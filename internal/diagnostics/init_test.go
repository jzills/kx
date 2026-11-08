package diagnostics

import (
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// initPod is a pod stuck before its app starts: one init container in the
// given state, and an app container waiting on it.
func initPod(init corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: init.Name}},
			Containers:     []corev1.Container{{Name: "app"}},
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{init},
			ContainerStatuses:     []corev1.ContainerStatus{waiting("app", "PodInitializing", "")},
		},
	}
}

// A pod in Init:CrashLoopBackOff used to report only that its app was waiting
// on PodInitializing and that it was pending, both warnings: the init
// container that was actually failing was never read, so the cause was
// missing from the report and the workload never reached critical.
func TestFailingInitContainerIsTheCause(t *testing.T) {
	crashing := waiting("migrate", "CrashLoopBackOff", "")
	crashing.RestartCount = 4
	got := summaries(podFindings(podDiagnostic(initPod(crashing)), unbounded))

	want := []string{"CrashLoopBackOff in init container migrate of Pod api-1 (4 restarts)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// The control for the test above: while an init container is still running,
// nothing has failed, and the pod reports what it reported before — waiting
// and pending. Without this, the suppression could be dropping those two
// findings for every pod.
func TestRunningInitContainerLeavesThePendingFindings(t *testing.T) {
	inProgress := running("migrate")
	inProgress.Ready = false
	got := summaries(podFindings(podDiagnostic(initPod(inProgress)), unbounded))

	want := []string{
		"Container app in Pod api-1 waiting: PodInitializing",
		"Pod api-1 pending",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// An init container that exited 0 did its job. A row for it would put a
// Completed line, and a log fetch, in every healthy pod's report; one that
// exited non-zero is kept, and marked as an init container.
func TestOnlyUnfinishedInitContainersAreKept(t *testing.T) {
	clean := podDiagnostic(initPod(terminated("migrate", "Completed", 0)))
	if len(clean.Containers) != 1 || clean.Containers[0].Name != "app" {
		t.Errorf("containers after a clean init = %+v, want app alone", clean.Containers)
	}

	failed := podDiagnostic(initPod(terminated("migrate", "Error", 1)))
	if len(failed.Containers) != 2 {
		t.Fatalf("containers after a failed init = %d, want 2", len(failed.Containers))
	}
	if first := failed.Containers[0]; first.Name != "migrate" || !first.Init {
		t.Errorf("first container = %+v, want migrate marked Init", first)
	}
	if failed.TotalContainers != 1 {
		t.Errorf("total = %d, want 1: an init container is not counted in READY",
			failed.TotalContainers)
	}
}

// A native sidecar (an init container with restartPolicy Always) runs beside
// the app for the pod's whole life, so kubectl counts it in READY, and so
// does kx. It is not an init step the pod is stuck on, so it is not marked.
func TestSidecarCountsTowardReady(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "proxy", RestartPolicy: &always}},
			Containers:     []corev1.Container{{Name: "app"}},
		},
		Status: corev1.PodStatus{
			Phase:                 corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{running("proxy")},
			ContainerStatuses:     []corev1.ContainerStatus{running("app")},
		},
	}
	got := podDiagnostic(pod)
	if got.ReadyContainers != 2 || got.TotalContainers != 2 {
		t.Errorf("ready/total = %d/%d, want 2/2", got.ReadyContainers, got.TotalContainers)
	}
	if len(got.Containers) != 2 || got.Containers[0].Init {
		t.Errorf("containers = %+v, want proxy unmarked beside app", got.Containers)
	}
}

// A Job pod that failed in its init container is dated by that container's
// exit, not by the Ready transition the fallback reads, which can be days
// apart for a pod that sat pending first.
func TestPodFailedInInitIsDatedByItsExit(t *testing.T) {
	exited := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	unready := exited.Add(-72 * time.Hour)

	status := terminated("migrate", "Error", 1)
	status.State.Terminated.FinishedAt = metav1.NewTime(exited)
	pod := initPod(status)
	pod.Status.Phase = corev1.PodFailed
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{waiting("app", "PodInitializing", "")}
	pod.Status.Conditions = []corev1.PodCondition{{
		Type: corev1.PodReady, Status: corev1.ConditionFalse,
		LastTransitionTime: metav1.NewTime(unready),
	}}

	if got := podDiagnostic(pod).StoppedAt(); !got.Equal(exited) {
		t.Errorf("StoppedAt = %v, want the init container's exit %v", got, exited)
	}
}
