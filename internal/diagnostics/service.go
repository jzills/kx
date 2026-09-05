package diagnostics

import (
	"context"
	"encoding/json"
	"strings"
	"time"

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
	// MaxAge bounds how long ago something may have happened and still be
	// reported: warning events, and the container and job history the
	// findings layer reads off the Data. Zero — the zero value, so a
	// Service built without one behaves as it always did — means no bound.
	// The CLI resolves the window from --since and the diag_max_age
	// setting and sets it here.
	MaxAge time.Duration
}

// New builds a diagnostics service over a Kubernetes client.
func New(client kubernetes.Interface) Service {
	return Service{
		Client: client,
		Events: events.APIService{Client: client},
		Graph:  graph.Builder{Client: client},
	}
}

// since is the instant this report's window opens, or the zero time when no
// window is set.
//
// Read once per gather and carried on the Data, so every signal in one report
// is measured against the same moment: two events read a moment apart cannot
// fall on opposite sides of a cutoff that moved between them, and the findings
// layer never has to look at a clock.
func (s Service) since() time.Time {
	if s.MaxAge <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-s.MaxAge)
}

// Gather collects everything known about one resource.
func (s Service) Gather(ctx context.Context, kind kinds.Kind, name, namespace string) (Data, error) {
	data := Data{
		Kind: kind, Name: name, Namespace: namespace,
		Since: s.since(), Window: s.MaxAge,
	}

	if err := s.attachKindHealth(ctx, &data, kind, name, namespace); err != nil {
		return Data{}, err
	}

	// Everything below is about a workload's own pods, and a Node has none in
	// that sense — attachKindHealth already tallied what is scheduled on it,
	// through the field selector the API server indexes.
	//
	// Run unguarded for a Node, this tail was three cluster-wide reads whose
	// results were all discarded: ResolveWorkloadPods listed every pod in the
	// cluster before falling to graph's `default: return nil, nil`, and
	// usageLookup then fetched every pod's metrics to attach them to the empty
	// slice that came back. On a large cluster that is tens of megabytes, and
	// a timeout, for one node's conditions.
	var pods []corev1.Pod
	if kind != kinds.Node {
		var err error
		pods, err = s.Graph.ResolveWorkloadPods(ctx, kind, name, namespace)
		if err != nil {
			return Data{}, err
		}
		for i := range pods {
			data.Pods = append(data.Pods, podDiagnostic(&pods[i]))
		}
		attachUsage(data.Pods, namespace, s.usageLookup(ctx, namespace))
		s.attachLogs(ctx, data.Pods, namespace, data.Since)
	}

	// Events are the exception: a Node's own warning events are the point of
	// diagnosing one, so this read stays.
	all, err := s.Events.Get(ctx, namespace)
	if err != nil {
		return Data{}, err
	}
	data.WarningEvents = s.warningEvents(data.Since, kind, name, namespace, pods, all)
	return data, nil
}

// attachKindHealth fills in the health rollup specific to the resource's kind.
func (s Service) attachKindHealth(
	ctx context.Context, data *Data, kind kinds.Kind, name, namespace string,
) error {
	get := metav1.GetOptions{}
	switch kind {
	case kinds.Node:
		node, err := s.Client.CoreV1().Nodes().Get(ctx, name, get)
		if err != nil {
			return err
		}
		health := &NodeHealth{Unschedulable: node.Spec.Unschedulable}
		for _, condition := range node.Status.Conditions {
			health.Conditions = append(health.Conditions, NodeCondition{
				Type:    string(condition.Type),
				Status:  string(condition.Status),
				Reason:  condition.Reason,
				Message: condition.Message,
			})
		}
		// Scoped by the field selector the API server indexes, so this asks
		// for the node's own pods rather than listing the cluster and
		// filtering. A failed read leaves the rollup empty rather than failing
		// the diagnosis: the conditions above are the diagnosis, and the pod
		// tally is context.
		pods, err := s.Client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + name,
		})
		if err == nil {
			health.Pods = podPhaseCounts(pods.Items)
		}
		data.Node = health
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
	case kinds.Ingress:
		ingress, err := s.Client.NetworkingV1().Ingresses(namespace).Get(ctx, name, get)
		if err != nil {
			return err
		}
		services, err := s.Client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(services.Items))
		for i := range services.Items {
			known[services.Items[i].Name] = true
		}
		var missing []string
		for _, backendName := range ingressBackendServiceNames(ingress) {
			if !known[backendName] {
				missing = append(missing, backendName)
			}
		}
		data.Ingress = &IngressHealth{MissingBackends: missing}
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
//
// Namespace-qualified because a cluster-wide sweep sees every namespace at
// once, and pod names are only unique within one. Two pods called web-abc in
// different namespaces would otherwise share a key, and whichever the metrics
// list returned last would supply usage figures for both — wrong numbers
// driving real findings.
type usageKey struct{ namespace, pod, container string }

type usageValue struct{ cpu, memory *resource.Quantity }

// usageLookup fetches container metrics for a namespace in one call, or for
// every namespace when the namespace is empty.
//
// Failure is swallowed — metrics-server not being installed is the same
// condition kx top surfaces directly, and it must not take the rest of kx diag
// down with it. Usage-based findings simply don't appear. That silence is why
// the all-namespaces path is spelled out rather than left to string
// concatenation: an empty namespace built ".../namespaces//pods", which the
// API server rejects, and every usage finding vanished cluster-wide without a
// word.
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

	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if namespace != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + namespace + "/pods"
	}
	raw, err := client.Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return lookup
	}

	var metrics struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
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
			lookup[usageKey{item.Metadata.Namespace, item.Metadata.Name, container.Name}] = value
		}
	}
	return lookup
}

func attachUsage(pods []PodDiagnostic, namespace string, lookup map[usageKey]usageValue) {
	for i := range pods {
		for j := range pods[i].Containers {
			container := &pods[i].Containers[j]
			if usage, ok := lookup[usageKey{namespace, pods[i].Name, container.Name}]; ok {
				container.CPUUsage = usage.cpu
				container.MemoryUsage = usage.memory
			}
		}
	}
}

// attachLogs fetches and filters a log excerpt for every unhealthy container.
func (s Service) attachLogs(
	ctx context.Context, pods []PodDiagnostic, namespace string, since time.Time,
) {
	for i := range pods {
		for j := range pods[i].Containers {
			container := &pods[i].Containers[j]
			if !containerNeedsLogs(*container, since) {
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
//
// Targets are namespace-qualified because Filter matches on name and kind
// alone, which identify an object only within a namespace. A cluster-wide sweep
// hands this every namespace's events at once, so an unscoped match would give
// a healthy resource its namesake's warnings — and warnings drive findings, so
// the row would surface as someone else's failure. An empty namespace matches
// anywhere, for a caller that doesn't know the scope it is asking about.
func (s Service) warningEvents(
	since time.Time, kind kinds.Kind, name, namespace string,
	pods []corev1.Pod, all []corev1.Event,
) []EventSummary {
	// Deduplicated: for a bare Pod the workload target equals its pod target.
	type target struct {
		name      string
		kind      kinds.Kind
		namespace string
	}
	first := target{name, kind, namespace}
	targets := []target{first}
	seen := map[target]bool{first: true}
	for i := range pods {
		key := target{pods[i].Name, kinds.Pod, pods[i].Namespace}
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
			if t.namespace != "" && eventNamespace(event) != t.namespace {
				continue
			}
			key := groupKey{event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name}
			count := events.Count(event)
			timestamp := events.Timestamp(event)
			// Dropped before grouping, so an occurrence outside the window
			// cannot inflate the ×count of one inside it. An event with no
			// timestamp at all is kept: it cannot be shown to be stale, and
			// hiding a live failure over a missing field is the worse error.
			if outsideWindow(timestamp, since) {
				continue
			}
			if existing, ok := groups[key]; ok {
				existing.Count += count
				// The newest occurrence, not the last one read: the API
				// returns events in no useful order, and this timestamp is
				// what the report prints as "most recent" and what the
				// window above is measured against. The message travels
				// with it so the two describe the same occurrence.
				if timestamp.After(existing.LastTimestamp) {
					existing.LastTimestamp = timestamp
					existing.Message = event.Message
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

// eventNamespace is the namespace an event belongs to.
//
// The involved object's reference is authoritative when set, but the API
// routinely leaves that field empty; the event's own metadata carries the
// namespace either way, since an event lives in the namespace of what it
// describes.
func eventNamespace(event corev1.Event) string {
	if event.InvolvedObject.Namespace != "" {
		return event.InvolvedObject.Namespace
	}
	return event.Namespace
}

// podPhaseCounts tallies a node's pods by phase.
//
// A phase kubernetes does not name — an empty string on a pod the API server
// has accepted but not yet placed — counts as Unknown rather than being
// dropped, so Total always equals the sum of the parts and "N/M not running"
// cannot exceed what is there.
func podPhaseCounts(pods []corev1.Pod) PodPhaseCounts {
	counts := PodPhaseCounts{Total: len(pods)}
	for i := range pods {
		switch pods[i].Status.Phase {
		case corev1.PodRunning:
			counts.Running++
		case corev1.PodPending:
			counts.Pending++
		case corev1.PodFailed:
			counts.Failed++
		case corev1.PodSucceeded:
			counts.Succeeded++
		default:
			counts.Unknown++
		}
	}
	return counts
}
