package diagnostics

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jzills/kx/internal/kinds"
)

func i32(v int32) *int32 { return &v }
func b(v bool) *bool     { return &v }

// running builds a container status for a healthy container.
func running(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}
}

func waiting(name, reason, message string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
			Reason: reason, Message: message,
		}},
	}
}

func terminated(name, reason string, exit int32) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: reason, ExitCode: exit,
		}},
	}
}

// A pod's counts come from its container statuses, and the phase falls back to
// Unknown — an empty phase would otherwise render as a blank cell.
func TestPodDiagnosticCountsAndPhase(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				running("app"), waiting("sidecar", "CrashLoopBackOff", ""),
			},
		},
	}
	got := podDiagnostic(pod)

	if got.Name != "web-1" || got.Node != "node-a" || got.Phase != "Running" {
		t.Errorf("diagnostic = %+v", got)
	}
	if got.ReadyContainers != 1 || got.TotalContainers != 2 {
		t.Errorf("ready/total = %d/%d, want 1/2", got.ReadyContainers, got.TotalContainers)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(got.Containers))
	}
	if got.Containers[1].WaitingReason != "CrashLoopBackOff" {
		t.Errorf("sidecar reason = %q", got.Containers[1].WaitingReason)
	}
}

func TestPodDiagnosticUnknownPhase(t *testing.T) {
	got := podDiagnostic(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}})
	if got.Phase != "Unknown" {
		t.Errorf("phase = %q, want Unknown", got.Phase)
	}
}

// The limits a percentage is measured against come from the spec, matched to
// the status by container name — a mismatch would silently drop them.
func TestContainerDiagnosticPairsSpecLimitsByName(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			}},
			{Name: "sidecar"},
		}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			running("sidecar"), running("app"),
		}},
	}
	got := podDiagnostic(pod)

	byName := map[string]ContainerDiagnostic{}
	for _, c := range got.Containers {
		byName[c.Name] = c
	}
	app := byName["app"]
	if app.CPULimit == nil || app.CPULimit.String() != "500m" {
		t.Errorf("app CPU limit = %v, want 500m", app.CPULimit)
	}
	if app.MemoryLimit == nil || app.MemoryLimit.String() != "256Mi" {
		t.Errorf("app memory limit = %v, want 256Mi", app.MemoryLimit)
	}
	// Deliberately listed second in the spec and first in the status: pairing by
	// position rather than by name would hand these limits to the wrong container.
	if byName["sidecar"].CPULimit != nil {
		t.Errorf("sidecar picked up a limit it does not declare")
	}
}

func TestContainerDiagnosticStates(t *testing.T) {
	tests := []struct {
		name   string
		status corev1.ContainerStatus
		state  string
		check  func(*testing.T, ContainerDiagnostic)
	}{
		{
			name: "running", status: running("app"), state: "Running",
			check: func(t *testing.T, c ContainerDiagnostic) {
				if !c.Ready {
					t.Error("running container is not ready")
				}
			},
		},
		{
			name:   "waiting carries reason and message",
			status: waiting("app", "ImagePullBackOff", "manifest unknown"), state: "Waiting",
			check: func(t *testing.T, c ContainerDiagnostic) {
				if c.WaitingReason != "ImagePullBackOff" || c.WaitingMessage != "manifest unknown" {
					t.Errorf("waiting = %q / %q", c.WaitingReason, c.WaitingMessage)
				}
			},
		},
		{
			name: "terminated carries exit code", status: terminated("app", "Error", 137),
			state: "Terminated",
			check: func(t *testing.T, c ContainerDiagnostic) {
				if c.ExitCode == nil || *c.ExitCode != 137 {
					t.Errorf("exit code = %v, want 137", c.ExitCode)
				}
			},
		},
		{
			name: "no state at all", status: corev1.ContainerStatus{Name: "app"}, state: "Unknown",
			check: func(*testing.T, ContainerDiagnostic) {},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := containerDiagnostic(&tc.status, nil)
			if got.State != tc.state {
				t.Errorf("state = %q, want %q", got.State, tc.state)
			}
			tc.check(t, got)
		})
	}
}

// The previous instance is where the reason a container died is written, so its
// termination has to survive into the diagnostic even while the container is
// waiting to restart.
func TestContainerDiagnosticKeepsLastTermination(t *testing.T) {
	status := waiting("app", "CrashLoopBackOff", "")
	status.LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137},
	}
	got := containerDiagnostic(&status, nil)

	if got.LastTerminatedReason != "OOMKilled" {
		t.Errorf("last terminated = %q, want OOMKilled", got.LastTerminatedReason)
	}
	if got.LastExitCode == nil || *got.LastExitCode != 137 {
		t.Errorf("last exit = %v, want 137", got.LastExitCode)
	}
}

func TestSchedulingInfo(t *testing.T) {
	unschedulable := corev1.PodStatus{Conditions: []corev1.PodCondition{{
		Type:    corev1.PodScheduled,
		Status:  corev1.ConditionFalse,
		Reason:  "Unschedulable",
		Message: "0/3 nodes are available: insufficient cpu",
	}}}
	got := schedulingInfo(unschedulable)
	if got.Schedulable {
		t.Error("Schedulable = true for a PodScheduled=False condition")
	}
	if got.Reason != "Unschedulable" || got.Message == "" {
		t.Errorf("scheduling = %+v", got)
	}

	scheduled := corev1.PodStatus{Conditions: []corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionTrue,
	}}}
	if !schedulingInfo(scheduled).Schedulable {
		t.Error("Schedulable = false for a scheduled pod")
	}
	// No condition at all is treated as schedulable rather than as a failure.
	if !schedulingInfo(corev1.PodStatus{}).Schedulable {
		t.Error("Schedulable = false with no PodScheduled condition")
	}
}

// A DaemonSet scales to nodes, not to a spec replica count, so its rollup reads
// entirely different status fields from the other two.
func TestReplicaHealthFromEachWorkloadKind(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       appsv1.DeploymentSpec{Replicas: i32(3)},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 2, ObservedGeneration: 3,
		},
	}
	got := replicaHealthFrom(kinds.Deployment, deployment)
	if got == nil || got.Desired != 3 || got.Ready != 1 || got.Updated != 2 {
		t.Fatalf("deployment health = %+v", got)
	}
	if got.Generation == nil || *got.Generation != 4 ||
		got.ObservedGeneration == nil || *got.ObservedGeneration != 3 {
		t.Errorf("generations = %v / %v", got.Generation, got.ObservedGeneration)
	}

	daemonSet := &appsv1.DaemonSet{Status: appsv1.DaemonSetStatus{
		DesiredNumberScheduled: 5, NumberReady: 4, NumberAvailable: 4, UpdatedNumberScheduled: 5,
	}}
	if got := replicaHealthFrom(kinds.DaemonSet, daemonSet); got == nil ||
		got.Desired != 5 || got.Ready != 4 {
		t.Errorf("daemonset health = %+v, want 4/5 ready", got)
	}

	statefulSet := &appsv1.StatefulSet{
		Spec:   appsv1.StatefulSetSpec{Replicas: i32(2)},
		Status: appsv1.StatefulSetStatus{ReadyReplicas: 2, AvailableReplicas: 2, UpdatedReplicas: 2},
	}
	if got := replicaHealthFrom(kinds.StatefulSet, statefulSet); got == nil || got.Desired != 2 {
		t.Errorf("statefulset health = %+v", got)
	}

	// A kind with no replica concept yields nothing rather than a zeroed rollup,
	// which BuildReport would read as "0/0 ready".
	if got := replicaHealthFrom(kinds.Pod, &corev1.Pod{}); got != nil {
		t.Errorf("pod health = %+v, want nil", got)
	}
}

// A nil spec.replicas means one, but the zero value is what the rollup carries;
// the point of this test is that it does not panic dereferencing it.
func TestReplicaHealthFromNilReplicas(t *testing.T) {
	got := replicaHealthFrom(kinds.Deployment, &appsv1.Deployment{})
	if got == nil || got.Desired != 0 {
		t.Errorf("health = %+v, want a zero desired count", got)
	}
}

func TestJobHealthFromConditions(t *testing.T) {
	job := &batchv1.Job{
		Spec: batchv1.JobSpec{BackoffLimit: i32(6), Suspend: b(true)},
		Status: batchv1.JobStatus{Succeeded: 0, Failed: 7, Active: 0, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Reason: "BackoffLimitExceeded"},
		}},
	}
	got := jobHealthFrom(job)
	if !got.BackoffLimitExceeded {
		t.Error("BackoffLimitExceeded = false")
	}
	if got.DeadlineExceeded {
		t.Error("DeadlineExceeded = true without the condition")
	}
	if got.Failed != 7 || got.BackoffLimit != 6 || !got.Suspended {
		t.Errorf("health = %+v", got)
	}

	deadline := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
		{Type: batchv1.JobFailed, Reason: "DeadlineExceeded"},
	}}}
	if !jobHealthFrom(deadline).DeadlineExceeded {
		t.Error("DeadlineExceeded = false with the condition set")
	}
}

func TestServiceHealthFromEndpoints(t *testing.T) {
	selected := &corev1.Service{Spec: corev1.ServiceSpec{
		Selector: map[string]string{"app": "web"},
	}}
	endpoints := &corev1.Endpoints{Subsets: []corev1.EndpointSubset{{
		Addresses:         []corev1.EndpointAddress{{IP: "10.0.0.1"}, {IP: "10.0.0.2"}},
		NotReadyAddresses: []corev1.EndpointAddress{{IP: "10.0.0.3"}},
	}}}
	got := serviceHealthFrom(selected, endpoints)
	if !got.HasSelector || got.ReadyAddresses != 2 || got.NotReadyAddresses != 1 {
		t.Errorf("health = %+v, want 2 ready / 1 not ready", got)
	}

	// Endpoints may legitimately be absent (manually managed, or the read 404ed);
	// that is zero addresses, not a crash.
	if got := serviceHealthFrom(selected, nil); got.ReadyAddresses != 0 {
		t.Errorf("health with no endpoints = %+v", got)
	}
	// A selectorless Service (ExternalName, headless) is a configuration, not a
	// defect, and the findings layer keys off exactly this flag.
	if serviceHealthFrom(&corev1.Service{}, nil).HasSelector {
		t.Error("HasSelector = true for a Service with no selector")
	}
}

// Logs are fetched only for containers with something wrong, so a healthy report
// stays fast and quiet.
func TestContainerNeedsLogs(t *testing.T) {
	healthy := ContainerDiagnostic{Ready: true, State: "Running"}
	if containerNeedsLogs(healthy) {
		t.Error("a healthy container was asked for logs")
	}
	for name, container := range map[string]ContainerDiagnostic{
		"not ready":       {Ready: false, State: "Running"},
		"not running":     {Ready: true, State: "Waiting"},
		"has restarted":   {Ready: true, State: "Running", RestartCount: 1},
		"died previously": {Ready: true, State: "Running", LastTerminatedReason: "Error"},
	} {
		if !containerNeedsLogs(container) {
			t.Errorf("%s: no logs requested", name)
		}
	}
}

func TestPhaseOr(t *testing.T) {
	if phaseOr("") != "Unknown" {
		t.Error("empty phase did not fall back to Unknown")
	}
	if phaseOr("Bound") != "Bound" {
		t.Error("a real phase was rewritten")
	}
}

func ingressBackend(serviceName string) networkingv1.IngressBackend {
	return networkingv1.IngressBackend{
		Service: &networkingv1.IngressServiceBackend{Name: serviceName},
	}
}

func ingressRule(paths ...networkingv1.HTTPIngressPath) networkingv1.IngressRule {
	return networkingv1.IngressRule{
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{Paths: paths},
		},
	}
}

func TestIngressBackendServiceNamesDefaultBackendOnly(t *testing.T) {
	ingress := &networkingv1.Ingress{Spec: networkingv1.IngressSpec{
		DefaultBackend: ingressBackendPtr("api"),
	}}
	got := ingressBackendServiceNames(ingress)
	if len(got) != 1 || got[0] != "api" {
		t.Errorf("names = %v, want [api]", got)
	}
}

func TestIngressBackendServiceNamesFromRules(t *testing.T) {
	ingress := &networkingv1.Ingress{Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{
			ingressRule(
				networkingv1.HTTPIngressPath{Backend: ingressBackend("web")},
				networkingv1.HTTPIngressPath{Backend: ingressBackend("api")},
			),
		},
	}}
	got := ingressBackendServiceNames(ingress)
	if len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Errorf("names = %v, want sorted [api web]", got)
	}
}

func TestIngressBackendServiceNamesDedupesAcrossDefaultAndRules(t *testing.T) {
	ingress := &networkingv1.Ingress{Spec: networkingv1.IngressSpec{
		DefaultBackend: ingressBackendPtr("api"),
		Rules: []networkingv1.IngressRule{
			ingressRule(networkingv1.HTTPIngressPath{Backend: ingressBackend("api")}),
		},
	}}
	got := ingressBackendServiceNames(ingress)
	if len(got) != 1 || got[0] != "api" {
		t.Errorf("names = %v, want deduped [api]", got)
	}
}

// A "resource" backend (e.g. a CRD-defined backend, not a Service) has no
// Service field and must be skipped without panicking — there is nothing to
// check existence of.
func TestIngressBackendServiceNamesSkipsResourceBackends(t *testing.T) {
	ingress := &networkingv1.Ingress{Spec: networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{
			ingressRule(networkingv1.HTTPIngressPath{
				Backend: networkingv1.IngressBackend{
					Resource: &corev1.TypedLocalObjectReference{Name: "not-a-service"},
				},
			}),
		},
	}}
	got := ingressBackendServiceNames(ingress)
	if len(got) != 0 {
		t.Errorf("names = %v, want none — a resource backend has no Service to check", got)
	}
}

func TestIngressBackendServiceNamesEmptyIngressYieldsNone(t *testing.T) {
	ingress := &networkingv1.Ingress{}
	if got := ingressBackendServiceNames(ingress); len(got) != 0 {
		t.Errorf("names = %v, want none", got)
	}
}

func ingressBackendPtr(serviceName string) *networkingv1.IngressBackend {
	backend := ingressBackend(serviceName)
	return &backend
}
