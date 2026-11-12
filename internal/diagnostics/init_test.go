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

// twoInitPod is initPod with a second init container queued behind the
// first, the way the kubelet reports it: Waiting, reason PodInitializing.
func twoInitPod(first corev1.ContainerStatus) *corev1.Pod {
	pod := initPod(first)
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{Name: "seed"})
	pod.Status.InitContainerStatuses = append(pod.Status.InitContainerStatuses,
		waiting("seed", "PodInitializing", ""))
	return pod
}

// An init container queued behind another is Waiting on PodInitializing, as
// the app is. It used to get a row and its own "waiting" warning, and that
// warning counted as a failure, hiding the pod's real state: the step that
// is running, and the pod pending on it.
func TestQueuedInitContainerSaysNothing(t *testing.T) {
	inProgress := running("migrate")
	inProgress.Ready = false
	diagnostic := podDiagnostic(twoInitPod(inProgress))

	for _, c := range diagnostic.Containers {
		if c.Name == "seed" {
			t.Errorf("queued init container has a row: %+v", c)
		}
	}
	got := summaries(podFindings(diagnostic, unbounded))
	want := []string{
		"Container app in Pod api-1 waiting: PodInitializing",
		"Pod api-1 pending",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// Behind a failing init container, the queued one adds nothing beside the
// critical that explains it.
func TestQueuedInitContainerBehindAFailureSaysNothing(t *testing.T) {
	crashing := waiting("migrate", "CrashLoopBackOff", "")
	got := summaries(podFindings(podDiagnostic(twoInitPod(crashing)), unbounded))
	want := []string{"CrashLoopBackOff in init container migrate of Pod api-1 (0 restarts)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// The kubelet reports PodInitializing for the init step that is blocked
// before it starts, a missing ConfigMap mount or a slow pull, as it does
// for the ones queued behind it. The blocked one used to lose its row with
// them, so the pod table named only the app. It keeps its row and adds no
// finding: what blocks it is in the events, not its status.
func TestBlockedInitStepKeepsItsRow(t *testing.T) {
	diagnostic := podDiagnostic(twoInitPod(waiting("migrate", "PodInitializing", "")))

	var names []string
	for _, c := range diagnostic.Containers {
		names = append(names, c.Name)
	}
	if want := []string{"migrate", "app"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("rows = %q, want %q: the blocked step, not the one queued behind it", names, want)
	}
	if !diagnostic.Containers[0].Init {
		t.Errorf("migrate = %+v, want it marked Init", diagnostic.Containers[0])
	}
	got := summaries(podFindings(diagnostic, unbounded))
	want := []string{
		"Container app in Pod api-1 waiting: PodInitializing",
		"Pod api-1 pending",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// A sidecar that has not started holds back the steps after it, so one
// waiting behind it is queued, not blocked.
func TestStepBehindAnUnstartedSidecarIsQueued(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	proxy := running("proxy")
	proxy.Started = b(false)
	pod := initPod(waiting("migrate", "PodInitializing", ""))
	pod.Spec.InitContainers = append([]corev1.Container{{Name: "proxy", RestartPolicy: &always}},
		pod.Spec.InitContainers...)
	pod.Status.InitContainerStatuses = append([]corev1.ContainerStatus{proxy},
		pod.Status.InitContainerStatuses...)

	for _, c := range podDiagnostic(pod).Containers {
		if c.Name == "migrate" {
			t.Errorf("step queued behind an unstarted sidecar has a row: %+v", c)
		}
	}
}

// A running init container near its memory limit has a finding and has
// failed nothing, so the pod is still reported pending on it. Keying the
// suppression on "produced a finding" hid both.
func TestRunningInitContainerWithAUsageWarningIsNotAFailure(t *testing.T) {
	pod := PodDiagnostic{
		Name: "api-1", Phase: "Pending", Scheduling: SchedulingInfo{Schedulable: true},
		Containers: []ContainerDiagnostic{
			{Name: "migrate", Init: true, State: "Running",
				MemoryUsage: quantity("85Mi"), MemoryLimit: quantity("100Mi")},
			{Name: "app", State: "Waiting", WaitingReason: "PodInitializing"},
		},
	}
	got := summaries(podFindings(pod, unbounded))
	want := []string{
		"Memory at 85% of limit (85Mi/100Mi)",
		"Container app in Pod api-1 waiting: PodInitializing",
		"Pod api-1 pending",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// A Job pod that failed in init a month ago is history under a 24h window:
// its init finding and "failed" are both outside it. The app still waits on
// PodInitializing, and that ongoing warning used to survive every window
// because suppression keyed on findings, which the window had emptied.
func TestOldInitFailureLeavesNothingInsideTheWindow(t *testing.T) {
	now := time.Now()
	status := terminated("migrate", "Error", 1)
	status.State.Terminated.FinishedAt = metav1.NewTime(now.Add(-30 * 24 * time.Hour))
	pod := initPod(status)
	pod.Status.Phase = corev1.PodFailed

	got := summaries(podFindings(podDiagnostic(pod), now.Add(-24*time.Hour)))
	if len(got) != 0 {
		t.Errorf("findings = %q, want none inside a 24h window", got)
	}
}

// An init container that got through only after OOMKills is not quiet: the
// kills are findings while the window holds them. It used to be dropped
// with every other exit 0.
func TestInitContainerThatSucceededAfterCrashingKeepsItsFindings(t *testing.T) {
	status := terminated("migrate", "Completed", 0)
	status.RestartCount = 3
	status.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
		Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
	}
	pod := initPod(status)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{running("app")}

	got := summaries(podFindings(podDiagnostic(pod), unbounded))
	want := []string{"OOMKilled in init container migrate of Pod api-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// A rebuilt sandbox runs every init container again, so one OOMKilled before
// the rebuild can be queued behind the first, waiting on PodInitializing.
// It used to be dropped with every other queued container, and the kill
// with it. Kept, it reports the kill and nothing about waiting its turn,
// and the pod is still pending on the step that is running.
func TestQueuedInitContainerKeepsAFailedPreviousRun(t *testing.T) {
	inProgress := running("migrate")
	inProgress.Ready = false
	pod := twoInitPod(inProgress)
	seed := &pod.Status.InitContainerStatuses[1]
	seed.RestartCount = 1
	seed.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
		Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
	}

	got := summaries(podFindings(podDiagnostic(pod), unbounded))
	want := []string{
		"OOMKilled in init container seed of Pod api-1",
		"Container app in Pod api-1 waiting: PodInitializing",
		"Pod api-1 pending",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

// A node reboot runs every init container again, each to a clean exit, and
// the restart count climbs. It used to keep the container's row on any
// count above zero, and past five, inside the window, warned that it
// restarted with no failure behind it. TestInitContainerThatSucceededAfter-
// CrashingKeepsItsFindings is the control: a failed previous run keeps it.
func TestCleanReRunsOfAnInitContainerAreQuiet(t *testing.T) {
	status := terminated("migrate", "Completed", 0)
	status.RestartCount = 6
	status.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
		Reason: "Completed", ExitCode: 0, FinishedAt: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
	}
	pod := initPod(status)
	pod.Status.Phase = corev1.PodRunning
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{running("app")}

	diagnostic := podDiagnostic(pod)
	if len(diagnostic.Containers) != 1 || diagnostic.Containers[0].Name != "app" {
		t.Errorf("containers = %+v, want app alone", diagnostic.Containers)
	}
	if got := summaries(podFindings(diagnostic, unbounded)); len(got) != 0 {
		t.Errorf("findings = %q, want none", got)
	}
}
