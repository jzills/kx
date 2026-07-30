package diagnostics

import (
	"context"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
)

// Service gathers diagnostic data from the API server.
type Service struct {
	Client kubernetes.Interface
	Events events.Service
	Graph  graph.Builder
}

// New builds a diagnostics service over a Kubernetes client.
func New(client kubernetes.Interface) Service {
	return Service{
		Client: client,
		Events: events.APIService{Client: client},
		Graph:  graph.Builder{Client: client},
	}
}

// Gather collects everything known about one resource.
func (s Service) Gather(ctx context.Context, kind kinds.Kind, name, namespace string) (Data, error) {
	data := Data{Kind: kind, Name: name, Namespace: namespace}

	if err := s.attachKindHealth(ctx, &data, kind, name, namespace); err != nil {
		return Data{}, err
	}

	pods, err := s.Graph.ResolveWorkloadPods(ctx, kind, name, namespace)
	if err != nil {
		return Data{}, err
	}
	for i := range pods {
		data.Pods = append(data.Pods, podDiagnostic(&pods[i]))
	}
	attachUsage(data.Pods, s.usageLookup(ctx, namespace))
	s.attachLogs(ctx, data.Pods, namespace)

	all, err := s.Events.Get(ctx, namespace)
	if err != nil {
		return Data{}, err
	}
	data.WarningEvents = s.warningEvents(kind, name, pods, all)
	return data, nil
}

// attachKindHealth fills in the health rollup specific to the resource's kind.
func (s Service) attachKindHealth(
	ctx context.Context, data *Data, kind kinds.Kind, name, namespace string,
) error {
	get := metav1.GetOptions{}
	switch kind {
	case kinds.Deployment:
		object, err := s.Client.AppsV1().Deployments(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		data.Replicas = replicaHealthFrom(kind, object)
	case kinds.StatefulSet:
		object, err := s.Client.AppsV1().StatefulSets(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		data.Replicas = replicaHealthFrom(kind, object)
	case kinds.DaemonSet:
		object, err := s.Client.AppsV1().DaemonSets(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		data.Replicas = replicaHealthFrom(kind, object)
	case kinds.Job:
		object, err := s.Client.BatchV1().Jobs(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		data.Job = jobHealthFrom(object)
	case kinds.Service:
		service, err := s.Client.CoreV1().Services(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		// A Service with manually managed Endpoints may legitimately have
		// none; a failed read is not a diagnostic failure.
		endpoints, err := s.Client.CoreV1().Endpoints(namespace).Get(ctx, name, get)
		if err != nil {
			endpoints = nil
		}
		data.Service = serviceHealthFrom(service, endpoints)
	case kinds.PersistentVolumeClaim:
		claim, err := s.Client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		data.PVC = &PVCHealth{Phase: phaseOr(string(claim.Status.Phase))}
	case kinds.CronJob:
		cronJob, err := s.Client.BatchV1().CronJobs(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		jobs, err := s.Client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		health := &CronJobHealth{Suspended: cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend}
		if recent := graph.MostRecentJob(cronJob.UID, jobs.Items); recent != nil {
			health.MostRecentJob = jobHealthFrom(recent)
		}
		data.CronJob = health
	}
	return nil
}

func phaseOr(phase string) string {
	if phase == "" {
		return "Unknown"
	}
	return phase
}

// usageKey identifies a container's metrics entry.
type usageKey struct{ pod, container string }

type usageValue struct{ cpu, memory *resource.Quantity }

// usageLookup fetches container metrics for a namespace in one call.
//
// Failure is swallowed — metrics-server not being installed is the same
// condition kx top surfaces directly, and it must not take the rest of kx diag
// down with it. Usage-based findings simply don't appear.
func (s Service) usageLookup(ctx context.Context, namespace string) map[usageKey]usageValue {
	lookup := map[usageKey]usageValue{}

	discovery := s.Client.Discovery()
	if discovery == nil {
		return lookup
	}
	client := discovery.RESTClient()
	if client == nil {
		return lookup
	}

	raw, err := client.Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces/" + namespace + "/pods").
		DoRaw(ctx)
	if err != nil {
		return lookup
	}

	var metrics struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Containers []struct {
				Name  string `json:"name"`
				Usage struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &metrics); err != nil {
		return lookup
	}

	for _, item := range metrics.Items {
		for _, container := range item.Containers {
			value := usageValue{}
			if cpu, err := resource.ParseQuantity(container.Usage.CPU); err == nil {
				value.cpu = &cpu
			}
			if memory, err := resource.ParseQuantity(container.Usage.Memory); err == nil {
				value.memory = &memory
			}
			lookup[usageKey{item.Metadata.Name, container.Name}] = value
		}
	}
	return lookup
}

func attachUsage(pods []PodDiagnostic, lookup map[usageKey]usageValue) {
	for i := range pods {
		for j := range pods[i].Containers {
			container := &pods[i].Containers[j]
			if usage, ok := lookup[usageKey{pods[i].Name, container.Name}]; ok {
				container.CPUUsage = usage.cpu
				container.MemoryUsage = usage.memory
			}
		}
	}
}

// attachLogs fetches and filters a log excerpt for every unhealthy container.
func (s Service) attachLogs(ctx context.Context, pods []PodDiagnostic, namespace string) {
	for i := range pods {
		for j := range pods[i].Containers {
			container := &pods[i].Containers[j]
			if !containerNeedsLogs(*container) {
				continue
			}
			raw, source := s.fetchLogTail(ctx, namespace, pods[i].Name, container.Name, container.State)
			if len(raw) == 0 {
				continue
			}
			container.LogLines, container.LogFiltered = FilterSeverityLines(raw)
			container.LogSource = source
		}
	}
}

// fetchLogTail returns the log lines and which instance they came from.
//
// A running container yields its current tail; a crashed or waiting one prefers
// the previous, dead instance — that's where the reason it died is written —
// falling back to current. Any API error (no previous logs yet, for instance)
// yields nothing rather than failing the diagnostic.
func (s Service) fetchLogTail(
	ctx context.Context, namespace, pod, container, state string,
) ([]string, string) {
	attempts := []bool{true, false}
	if state == "Running" {
		attempts = []bool{false}
	}

	tail := int64(logTailLines)
	for _, previous := range attempts {
		raw, err := s.Client.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
			Container: container,
			Previous:  previous,
			TailLines: &tail,
		}).DoRaw(ctx)
		if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		source := "current"
		if previous {
			source = "previous"
		}
		return strings.Split(strings.TrimRight(string(raw), "\n"), "\n"), source
	}
	return nil, ""
}

// warningEvents groups a resource's warning events, and those of its pods, by
// reason and involved object.
func (s Service) warningEvents(
	kind kinds.Kind, name string, pods []corev1.Pod, all []corev1.Event,
) []EventSummary {
	// Deduplicated: for a bare Pod the workload target equals its pod target.
	type target struct {
		name string
		kind kinds.Kind
	}
	targets := []target{{name, kind}}
	seen := map[target]bool{{name, kind}: true}
	for i := range pods {
		key := target{pods[i].Name, kinds.Pod}
		if !seen[key] {
			seen[key] = true
			targets = append(targets, key)
		}
	}

	type groupKey struct{ reason, kind, name string }
	// Grouped, but insertion-ordered: the sweep's output must not reshuffle
	// between runs.
	groups := map[groupKey]*EventSummary{}
	var order []groupKey

	for _, t := range targets {
		for _, event := range s.Events.Filter(all, t.name, t.kind) {
			if event.Type != "Warning" {
				continue
			}
			key := groupKey{event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name}
			count := events.Count(event)
			timestamp := events.Timestamp(event)
			if existing, ok := groups[key]; ok {
				existing.Count += count
				existing.Message = event.Message
				if !timestamp.IsZero() {
					existing.LastTimestamp = timestamp
				}
				continue
			}
			groups[key] = &EventSummary{
				Reason:        event.Reason,
				Message:       event.Message,
				Kind:          event.InvolvedObject.Kind,
				Name:          event.InvolvedObject.Name,
				Count:         count,
				LastTimestamp: timestamp,
			}
			order = append(order, key)
		}
	}

	summaries := make([]EventSummary, 0, len(order))
	for _, key := range order {
		summaries = append(summaries, *groups[key])
	}
	return summaries
}
