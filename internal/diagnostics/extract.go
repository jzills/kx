package diagnostics

import (
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/kinds"
)

// OTEL-aligned severity tokens used to surface error lines from container logs.
var severityPattern = regexp.MustCompile(
	`(?i)\b(FATAL|CRITICAL|ERROR|ERR|WARNING|WARN|EXCEPTION|TRACEBACK|PANIC)\b`)

const (
	logTailLines     = 50 // lines requested from the API per container
	logMaxMatches    = 8  // severity-matching lines shown
	logFallbackLines = 3  // raw tail shown when nothing matches
)

// FilterSeverityLines selects the most relevant log lines: the last few matching
// an OTEL severity token, or — when none match — the last few raw lines, so a
// failing container always shows something.
//
// The bool reports whether the lines matched a severity token, which the
// renderer uses to label a raw tail as such.
func FilterSeverityLines(raw []string) ([]string, bool) {
	var lines, matches []string
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			continue
		}
		line = strings.TrimRight(line, " \t\r\n")
		lines = append(lines, line)
		if severityPattern.MatchString(line) {
			matches = append(matches, line)
		}
	}
	if len(matches) > 0 {
		return lastN(matches, logMaxMatches), true
	}
	return lastN(lines, logFallbackLines), false
}

func lastN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

// replicaHealthFrom extracts replica health from an already-fetched workload.
func replicaHealthFrom(kind kinds.Kind, object any) *ReplicaHealth {
	switch workload := object.(type) {
	case *appsv1.Deployment:
		return &ReplicaHealth{
			Desired:            derefInt32(workload.Spec.Replicas),
			Ready:              workload.Status.ReadyReplicas,
			Available:          workload.Status.AvailableReplicas,
			Updated:            workload.Status.UpdatedReplicas,
			Generation:         int64Ptr(workload.Generation),
			ObservedGeneration: int64Ptr(workload.Status.ObservedGeneration),
		}
	case *appsv1.StatefulSet:
		return &ReplicaHealth{
			Desired:            derefInt32(workload.Spec.Replicas),
			Ready:              workload.Status.ReadyReplicas,
			Available:          workload.Status.AvailableReplicas,
			Updated:            workload.Status.UpdatedReplicas,
			Generation:         int64Ptr(workload.Generation),
			ObservedGeneration: int64Ptr(workload.Status.ObservedGeneration),
		}
	case *appsv1.DaemonSet:
		// A DaemonSet's replica counts live under different field names: it
		// scales to nodes, not to a spec replica count.
		return &ReplicaHealth{
			Desired:            workload.Status.DesiredNumberScheduled,
			Ready:              workload.Status.NumberReady,
			Available:          workload.Status.NumberAvailable,
			Updated:            workload.Status.UpdatedNumberScheduled,
			Generation:         int64Ptr(workload.Generation),
			ObservedGeneration: int64Ptr(workload.Status.ObservedGeneration),
		}
	}
	return nil
}

// jobHealthFrom extracts job health from an already-fetched Job.
func jobHealthFrom(job *batchv1.Job) *JobHealth {
	failedReasons := map[string]bool{}
	// The Failed condition's transition is the only date a failed run has:
	// CompletionTime is set on success, and StartTime says when it began
	// rather than when it went wrong.
	var failedAt time.Time
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed {
			failedReasons[condition.Reason] = true
			if at := condition.LastTransitionTime.Time; at.After(failedAt) {
				failedAt = at
			}
		}
	}
	return &JobHealth{
		FailedAt:             failedAt,
		Succeeded:            job.Status.Succeeded,
		Failed:               job.Status.Failed,
		Active:               job.Status.Active,
		Suspended:            job.Spec.Suspend != nil && *job.Spec.Suspend,
		BackoffLimit:         derefInt32(job.Spec.BackoffLimit),
		BackoffLimitExceeded: failedReasons["BackoffLimitExceeded"],
		DeadlineExceeded:     failedReasons["DeadlineExceeded"],
	}
}

// serviceHealthFrom extracts service health from a Service and its Endpoints.
// Endpoints may be nil when the read failed or 404ed.
func serviceHealthFrom(service *corev1.Service, endpoints *corev1.Endpoints) *ServiceHealth {
	health := &ServiceHealth{HasSelector: len(service.Spec.Selector) > 0}
	if endpoints == nil {
		return health
	}
	for _, subset := range endpoints.Subsets {
		health.ReadyAddresses += len(subset.Addresses)
		health.NotReadyAddresses += len(subset.NotReadyAddresses)
	}
	return health
}

// ingressBackendServiceNames returns the deduped, sorted Service names
// referenced by an Ingress's default backend and every rule's paths. A
// "resource" backend (non-Service — e.g. a CRD-defined backend) has no
// Service name and is skipped: existence can't be checked the same way, and
// there's nothing to report against.
func ingressBackendServiceNames(ingress *networkingv1.Ingress) []string {
	seen := map[string]bool{}
	var names []string
	add := func(backend networkingv1.IngressBackend) {
		if backend.Service == nil || backend.Service.Name == "" {
			return
		}
		if !seen[backend.Service.Name] {
			seen[backend.Service.Name] = true
			names = append(names, backend.Service.Name)
		}
	}
	if ingress.Spec.DefaultBackend != nil {
		add(*ingress.Spec.DefaultBackend)
	}
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			add(path.Backend)
		}
	}
	sort.Strings(names)
	return names
}

func podDiagnostic(pod *corev1.Pod) PodDiagnostic {
	specContainers := map[string]*corev1.Container{}
	for i := range pod.Spec.Containers {
		specContainers[pod.Spec.Containers[i].Name] = &pod.Spec.Containers[i]
	}

	ready := 0
	containers := make([]ContainerDiagnostic, 0, len(pod.Status.ContainerStatuses))
	for i := range pod.Status.ContainerStatuses {
		status := &pod.Status.ContainerStatuses[i]
		if status.Ready {
			ready++
		}
		containers = append(containers, containerDiagnostic(status, specContainers[status.Name]))
	}

	phase := string(pod.Status.Phase)
	if phase == "" {
		phase = "Unknown"
	}
	return PodDiagnostic{
		Name:            pod.Name,
		Phase:           phase,
		Node:            pod.Spec.NodeName,
		ReadyContainers: ready,
		TotalContainers: len(pod.Status.ContainerStatuses),
		Containers:      containers,
		Scheduling:      schedulingInfo(pod.Status),
	}
}

func containerDiagnostic(status *corev1.ContainerStatus, spec *corev1.Container) ContainerDiagnostic {
	diagnostic := ContainerDiagnostic{
		Name:         status.Name,
		Ready:        status.Ready,
		Started:      status.Started,
		RestartCount: status.RestartCount,
		State:        "Unknown",
		LogFiltered:  true,
	}

	switch {
	case status.State.Running != nil:
		diagnostic.State = "Running"
	case status.State.Waiting != nil:
		diagnostic.State = "Waiting"
		diagnostic.WaitingReason = status.State.Waiting.Reason
		diagnostic.WaitingMessage = status.State.Waiting.Message
	case status.State.Terminated != nil:
		diagnostic.State = "Terminated"
		diagnostic.TerminatedReason = status.State.Terminated.Reason
		diagnostic.ExitCode = int32Ptr(status.State.Terminated.ExitCode)
		diagnostic.TerminatedAt = status.State.Terminated.FinishedAt.Time
	}

	if last := status.LastTerminationState.Terminated; last != nil {
		diagnostic.LastTerminatedReason = last.Reason
		diagnostic.LastExitCode = int32Ptr(last.ExitCode)
		diagnostic.LastTerminatedAt = last.FinishedAt.Time
	}

	if spec != nil {
		if limit, ok := spec.Resources.Limits[corev1.ResourceCPU]; ok {
			diagnostic.CPULimit = quantityPtr(limit)
		}
		if limit, ok := spec.Resources.Limits[corev1.ResourceMemory]; ok {
			diagnostic.MemoryLimit = quantityPtr(limit)
		}
	}
	return diagnostic
}

func schedulingInfo(status corev1.PodStatus) SchedulingInfo {
	for _, condition := range status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status != corev1.ConditionTrue {
			return SchedulingInfo{
				Schedulable: false,
				Reason:      condition.Reason,
				Message:     condition.Message,
			}
		}
	}
	return SchedulingInfo{Schedulable: true}
}

// containerNeedsLogs reports whether a container is unhealthy in some way: not
// ready, not running, restarted, or previously terminated. Fully healthy
// containers are skipped so healthy reports stay clean and fast.
//
// A container whose only trouble is outside the window is skipped too. Nothing
// will report that history, so the previous instance's tail would be an
// excerpt of a crash no finding mentions — and one API call per container to
// produce it.
func containerNeedsLogs(container ContainerDiagnostic, since time.Time) bool {
	// A container that stopped before the window opened is one no finding
	// will mention, so its tail would be an excerpt of a crash the report
	// does not report.
	if outsideWindow(container.TerminatedAt, since) {
		return false
	}
	if !container.Ready || container.State != "Running" {
		return true
	}
	if outsideWindow(container.LastTerminatedAt, since) {
		return false
	}
	return container.RestartCount > 0 || container.LastTerminatedReason != ""
}

func derefInt32(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func int32Ptr(value int32) *int32 { return &value }

func int64Ptr(value int64) *int64 { return &value }

func quantityPtr(value resource.Quantity) *resource.Quantity { return &value }
