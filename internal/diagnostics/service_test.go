package diagnostics

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/kinds"
)

const ns = "prod"

func meta(name string, uid types.UID, owners ...metav1.OwnerReference) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: ns, UID: uid, OwnerReferences: owners}
}

func owner(uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{UID: uid}
}

func service(objects ...runtime.Object) Service {
	return New(fake.NewSimpleClientset(objects...))
}

// podWith builds a pod with the given container statuses already flattened.
func podWith(name string, uid types.UID, phase corev1.PodPhase,
	statuses []corev1.ContainerStatus, owners ...metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: meta(name, uid, owners...),
		Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
	}
}

func warning(name, reason, kind, object string, count int32) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:           "Warning",
		Reason:         reason,
		Message:        reason + " happened",
		Count:          count,
		LastTimestamp:  metav1.NewTime(time.Now().Add(-time.Minute)),
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: object},
	}
}

// A Deployment gather is a two-hop walk for its pods plus a replica rollup, and
// the two have to arrive on the same Data.
func TestGatherDeployment(t *testing.T) {
	s := service(
		&appsv1.Deployment{
			ObjectMeta: meta("web", "d1"),
			Spec:       appsv1.DeploymentSpec{Replicas: i32(3)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", corev1.PodRunning,
			[]corev1.ContainerStatus{waiting("app", "CrashLoopBackOff", "")}, owner("rs1")),
		// Owned by something else entirely; must not be gathered.
		podWith("other-1", "p2", corev1.PodRunning,
			[]corev1.ContainerStatus{running("app")}, owner("rs9")),
	)

	data, err := s.Gather(context.Background(), kinds.Deployment, "web", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if data.Replicas == nil || data.Replicas.Desired != 3 || data.Replicas.Ready != 1 {
		t.Errorf("replicas = %+v, want 1/3 ready", data.Replicas)
	}
	if len(data.Pods) != 1 || data.Pods[0].Name != "web-abc-1" {
		t.Fatalf("pods = %+v, want only web-abc-1", data.Pods)
	}
	if data.Kind != kinds.Deployment || data.Name != "web" || data.Namespace != ns {
		t.Errorf("identity = %s/%s in %s", data.Kind, data.Name, data.Namespace)
	}
}

func TestGatherJobServiceAndPVC(t *testing.T) {
	t.Run("job", func(t *testing.T) {
		s := service(&batchv1.Job{
			ObjectMeta: meta("import", "j1"),
			Spec:       batchv1.JobSpec{BackoffLimit: i32(3)},
			Status: batchv1.JobStatus{Failed: 4, Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Reason: "BackoffLimitExceeded"},
			}},
		})
		data, err := s.Gather(context.Background(), kinds.Job, "import", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.Job == nil || !data.Job.BackoffLimitExceeded {
			t.Errorf("job health = %+v", data.Job)
		}
	})

	t.Run("service reads endpoints", func(t *testing.T) {
		s := service(
			&corev1.Service{
				ObjectMeta: meta("api", "s1"),
				Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
			},
			&corev1.Endpoints{
				ObjectMeta: meta("api", "e1"),
				Subsets: []corev1.EndpointSubset{{
					Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				}},
			},
		)
		data, err := s.Gather(context.Background(), kinds.Service, "api", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.Service == nil || data.Service.ReadyAddresses != 1 {
			t.Errorf("service health = %+v, want 1 ready address", data.Service)
		}
	})

	// A Service whose Endpoints object is absent is a Service with no endpoints,
	// not a failed gather.
	t.Run("service without endpoints still gathers", func(t *testing.T) {
		s := service(&corev1.Service{
			ObjectMeta: meta("api", "s1"),
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
		})
		data, err := s.Gather(context.Background(), kinds.Service, "api", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.Service == nil || data.Service.ReadyAddresses != 0 {
			t.Errorf("service health = %+v", data.Service)
		}
	})

	t.Run("pvc", func(t *testing.T) {
		s := service(&corev1.PersistentVolumeClaim{
			ObjectMeta: meta("data", "v1"),
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
		})
		data, err := s.Gather(context.Background(), kinds.PersistentVolumeClaim, "data", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.PVC == nil || data.PVC.Phase != "Pending" {
			t.Errorf("pvc health = %+v", data.PVC)
		}
	})
}

// A CronJob's health is the latest run, not the retained history — an old
// failure must not outrank a recent success.
func TestGatherCronJobUsesTheMostRecentRun(t *testing.T) {
	old := &batchv1.Job{
		ObjectMeta: meta("nightly-1", "j1", owner("c1")),
		Status: batchv1.JobStatus{Failed: 5, Conditions: []batchv1.JobCondition{
			{Type: batchv1.JobFailed, Reason: "BackoffLimitExceeded"},
		}},
	}
	old.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Hour))
	recent := &batchv1.Job{
		ObjectMeta: meta("nightly-2", "j2", owner("c1")),
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	recent.CreationTimestamp = metav1.NewTime(time.Now())

	s := service(&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")}, old, recent)

	data, err := s.Gather(context.Background(), kinds.CronJob, "nightly", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if data.CronJob == nil || data.CronJob.MostRecentJob == nil {
		t.Fatalf("cronjob health = %+v", data.CronJob)
	}
	if data.CronJob.MostRecentJob.BackoffLimitExceeded {
		t.Error("the older failed run was reported instead of the recent success")
	}
	if data.CronJob.MostRecentJob.Succeeded != 1 {
		t.Errorf("most recent run = %+v, want the succeeded one", data.CronJob.MostRecentJob)
	}
}

func TestGatherCronJobSuspended(t *testing.T) {
	s := service(&batchv1.CronJob{
		ObjectMeta: meta("nightly", "c1"),
		Spec:       batchv1.CronJobSpec{Suspend: b(true)},
	})
	data, err := s.Gather(context.Background(), kinds.CronJob, "nightly", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if data.CronJob == nil || !data.CronJob.Suspended {
		t.Errorf("cronjob health = %+v, want suspended", data.CronJob)
	}
	// Never run is not a failure — there is no most-recent job to report on.
	if data.CronJob.MostRecentJob != nil {
		t.Errorf("most recent job = %+v, want nil", data.CronJob.MostRecentJob)
	}
}

func TestGatherMissingResourceIsAnError(t *testing.T) {
	if _, err := service().Gather(context.Background(), kinds.Deployment, "nope", ns); err == nil {
		t.Fatal("Gather on a missing Deployment returned no error")
	}
}

// Warning events are collected for the workload and for each of its pods, and
// grouped by reason and involved object so a repeated event is one row.
func TestGatherCollectsAndGroupsWarningEvents(t *testing.T) {
	s := service(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", corev1.PodPending, nil, owner("rs1")),
		warning("e1", "FailedScheduling", "Pod", "web-abc-1", 3),
		warning("e2", "FailedScheduling", "Pod", "web-abc-1", 2),
		warning("e3", "ScalingReplicaSet", "Deployment", "web", 1),
		// Normal events are not warnings and must not appear.
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e4", Namespace: ns},
			Type:           "Normal",
			Reason:         "Scheduled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-abc-1"},
		},
		// An event about something else entirely.
		warning("e5", "BackOff", "Pod", "unrelated", 9),
	)

	data, err := s.Gather(context.Background(), kinds.Deployment, "web", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	byReason := map[string]EventSummary{}
	for _, event := range data.WarningEvents {
		byReason[event.Reason] = event
	}
	if len(data.WarningEvents) != 2 {
		t.Fatalf("events = %+v, want FailedScheduling and ScalingReplicaSet", data.WarningEvents)
	}
	// Two events, same reason and object: one row carrying the summed count.
	if got := byReason["FailedScheduling"].Count; got != 5 {
		t.Errorf("FailedScheduling count = %d, want 5", got)
	}
	if _, ok := byReason["Scheduled"]; ok {
		t.Error("a Normal event was reported as a warning")
	}
	if _, ok := byReason["BackOff"]; ok {
		t.Error("an unrelated object's event was reported")
	}
}

// A bare Pod is its own workload target and its own pod target; without
// deduplication its events would be counted twice.
func TestGatherPodDoesNotDoubleCountItsOwnEvents(t *testing.T) {
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		warning("e1", "Unhealthy", "Pod", "solo", 4),
	)
	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 1 {
		t.Fatalf("events = %+v, want one", data.WarningEvents)
	}
	if data.WarningEvents[0].Count != 4 {
		t.Errorf("count = %d, want 4 — the event was counted once, not per target",
			data.WarningEvents[0].Count)
	}
}

// metrics.k8s.io is not served by the fake clientset, which is the same
// condition as a cluster with no metrics-server. It must degrade to no usage
// data rather than failing the gather.
func TestUsageLookupWithoutMetricsServerIsEmpty(t *testing.T) {
	s := service()
	if got := s.usageLookup(context.Background(), ns); len(got) != 0 {
		t.Errorf("usage = %+v, want empty", got)
	}
}

func TestAttachUsageMatchesOnPodAndContainer(t *testing.T) {
	pods := []PodDiagnostic{{
		Name:       "web-1",
		Containers: []ContainerDiagnostic{{Name: "app"}, {Name: "sidecar"}},
	}}
	cpu := mustQuantity(t, "250m")
	lookup := map[usageKey]usageValue{
		{namespace: ns, pod: "web-1", container: "app"}: {cpu: cpu},
		// A container in another pod with the same name must not match.
		{namespace: ns, pod: "other", container: "sidecar"}: {cpu: cpu},
	}
	attachUsage(pods, ns, lookup)

	if pods[0].Containers[0].CPUUsage == nil {
		t.Error("app got no usage")
	}
	if pods[0].Containers[1].CPUUsage != nil {
		t.Error("sidecar picked up another pod's usage")
	}
}

// A cluster-wide sweep sees every namespace at once, and pod names are only
// unique within one. Usage must not cross that boundary, or two pods sharing a
// name would report each other's figures — and usage drives real findings.
func TestAttachUsageDoesNotCrossNamespaces(t *testing.T) {
	pods := []PodDiagnostic{{
		Name:       "web-1",
		Containers: []ContainerDiagnostic{{Name: "app"}},
	}}
	lookup := map[usageKey]usageValue{
		{namespace: "elsewhere", pod: "web-1", container: "app"}: {
			cpu: mustQuantity(t, "250m"),
		},
	}
	attachUsage(pods, ns, lookup)

	if pods[0].Containers[0].CPUUsage != nil {
		t.Error("app took usage from a same-named pod in another namespace")
	}
}

func mustQuantity(t *testing.T, s string) *resource.Quantity {
	t.Helper()
	q, err := resource.ParseQuantity(s)
	if err != nil {
		t.Fatalf("ParseQuantity(%q): %v", s, err)
	}
	return &q
}
