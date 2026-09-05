package diagnostics

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

	t.Run("ingress reports missing backends", func(t *testing.T) {
		s := service(
			&networkingv1.Ingress{
				ObjectMeta: meta("web", "i1"),
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{{
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{Name: "api"},
									}},
								},
							},
						},
					}},
				},
			},
			&corev1.Service{ObjectMeta: meta("web-frontend", "s1")},
		)
		data, err := s.Gather(context.Background(), kinds.Ingress, "web", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.Ingress == nil || len(data.Ingress.MissingBackends) != 1 ||
			data.Ingress.MissingBackends[0] != "api" {
			t.Errorf("ingress health = %+v, want MissingBackends [api]", data.Ingress)
		}
	})

	t.Run("ingress with an existing backend has no missing backends", func(t *testing.T) {
		s := service(
			&networkingv1.Ingress{
				ObjectMeta: meta("web", "i1"),
				Spec: networkingv1.IngressSpec{
					DefaultBackend: &networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{Name: "api"},
					},
				},
			},
			&corev1.Service{ObjectMeta: meta("api", "s1")},
		)
		data, err := s.Gather(context.Background(), kinds.Ingress, "web", ns)
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		if data.Ingress == nil || len(data.Ingress.MissingBackends) != 0 {
			t.Errorf("ingress health = %+v, want no missing backends", data.Ingress)
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

// Gather's tail resolves a workload's pods, attaches their usage and reads
// their logs. A Node has no pods in that sense — attachKindHealth already
// tallied what is scheduled on it, using the field selector the API server
// indexes — but the tail ran anyway, listing every pod in the cluster and then
// throwing the list away at graph's `default: return nil, nil`.
//
// Counting pod lists is what sees it: the field-selected one is legitimate and
// the unrestricted one is the defect, so the shape of the assertion has to be
// "which lists", not "how many reads".
func TestGatherNodeDoesNotListEveryPodInTheCluster(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		podWith("web", "uid-web", corev1.PodRunning, nil),
	)
	if _, err := New(client).Gather(context.Background(), kinds.Node, "node-a", ""); err != nil {
		t.Fatalf("Gather: %v", err)
	}

	var scoped, unrestricted int
	for _, action := range client.Actions() {
		list, ok := action.(k8stesting.ListAction)
		if !ok || action.GetResource().Resource != "pods" {
			continue
		}
		if list.GetListRestrictions().Fields.Empty() {
			unrestricted++
		} else {
			scoped++
		}
	}
	if unrestricted != 0 {
		t.Errorf("Gather made %d unrestricted pod list(s); a Node's pods come from the spec.nodeName selector", unrestricted)
	}
	if scoped != 1 {
		t.Errorf("Gather made %d field-selected pod list(s), want exactly 1", scoped)
	}
}

// The node's own warning events are the point of diagnosing one, so that read
// must survive the guard above.
func TestGatherNodeStillReadsWarningEvents(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		warning("node-a.1", "NodeNotReady", "Node", "node-a", 3),
	)
	data, err := New(client).Gather(context.Background(), kinds.Node, "node-a", "")
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) == 0 {
		t.Error("Gather returned no warning events for a Node that has one")
	}
}

// A workload still resolves its pods — the guard is for Nodes only, and must
// not have quietly emptied the pod table every other kind depends on.
func TestGatherWorkloadStillResolvesItsPods(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: meta("web", "uid-deploy")}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "uid-rs", owner("uid-deploy"))}
	pod := podWith("web-abc-1", "uid-pod", corev1.PodRunning, nil, owner("uid-rs"))

	data, err := service(deployment, replicaSet, pod).
		Gather(context.Background(), kinds.Deployment, "web", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.Pods) != 1 {
		t.Fatalf("got %d pods, want 1", len(data.Pods))
	}
}

// agedWarning is a warning event last seen a given time ago.
func agedWarning(name, reason, kind, object string, count int32, age time.Duration) *corev1.Event {
	event := warning(name, reason, kind, object, count)
	event.LastTimestamp = metav1.NewTime(time.Now().Add(-age))
	return event
}

// The point of the window: a warning from three weeks ago is not what is wrong
// with a workload today, and while it was reported it held the verdict at
// "warnings" — and any --fail-on gate red — indefinitely.
func TestGatherDropsEventsOlderThanTheWindow(t *testing.T) {
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		agedWarning("e1", "Unhealthy", "Pod", "solo", 1, 10*time.Minute),
		agedWarning("e2", "FailedScheduling", "Pod", "solo", 1, 21*24*time.Hour),
	)
	s.MaxAge = time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 1 || data.WarningEvents[0].Reason != "Unhealthy" {
		t.Fatalf("events = %+v, want only the 10m-old Unhealthy", data.WarningEvents)
	}
}

// Zero is "no window", and it has to stay the zero value's meaning: every
// caller that builds a Service without setting one gets today's behaviour.
func TestGatherWithoutAWindowKeepsEveryEvent(t *testing.T) {
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		agedWarning("e1", "Unhealthy", "Pod", "solo", 1, 10*time.Minute),
		agedWarning("e2", "FailedScheduling", "Pod", "solo", 1, 21*24*time.Hour),
	)

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 2 {
		t.Fatalf("events = %+v, want both", data.WarningEvents)
	}
}

// An event carrying neither a last-seen nor a creation time cannot be shown to
// be stale, and dropping it would hide a live failure on the strength of a
// missing field. The renderer already omits the age for one of these.
func TestGatherKeepsAnEventWithNoTimestamp(t *testing.T) {
	undated := warning("e1", "Unhealthy", "Pod", "solo", 1)
	undated.LastTimestamp = metav1.Time{}
	undated.CreationTimestamp = metav1.Time{}
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		undated,
	)
	s.MaxAge = time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 1 {
		t.Fatalf("events = %+v, want the undated event kept", data.WarningEvents)
	}
}

// Filtered before grouping, not after: an occurrence outside the window must
// not inflate the ×count of the one inside it, which is the number the finding
// and the report both print.
func TestGatherDoesNotCountFilteredOccurrencesInATally(t *testing.T) {
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		agedWarning("e1", "Unhealthy", "Pod", "solo", 2, 10*time.Minute),
		agedWarning("e2", "Unhealthy", "Pod", "solo", 40, 21*24*time.Hour),
	)
	s.MaxAge = time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 1 {
		t.Fatalf("events = %+v, want one", data.WarningEvents)
	}
	if got := data.WarningEvents[0].Count; got != 2 {
		t.Errorf("count = %d, want 2 — the 40 occurrences outside the window are not this week's", got)
	}
}

// A sweep reads the same events through the same helper, so the window has to
// hold there too — a namespace triage is where a stale warning does the most
// damage, since it is one row of many nobody re-reads.
func TestSweepDropsEventsOlderThanTheWindow(t *testing.T) {
	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		agedWarning("e1", "FailedScheduling", "Pod", "solo", 1, 21*24*time.Hour),
	)
	s.MaxAge = time.Hour

	all, err := s.Sweep(context.Background(), ns)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("Sweep returned nothing to check")
	}
	for _, data := range all {
		if len(data.WarningEvents) != 0 {
			t.Errorf("%s/%s events = %+v, want none", data.Kind, data.Name, data.WarningEvents)
		}
	}
}

// The grouped summary's timestamp is what the report prints as the most recent
// occurrence, and what the window is measured against. The API returns events
// in no particular order, so taking whichever arrived last reported an older
// occurrence as the newest.
func TestGroupedEventKeepsTheNewestOccurrence(t *testing.T) {
	newest := time.Now().Add(-5 * time.Minute)
	old := warning("e2", "Unhealthy", "Pod", "solo", 1)
	old.LastTimestamp = metav1.NewTime(time.Now().Add(-6 * time.Hour))
	old.Message = "the old one"
	recent := warning("e1", "Unhealthy", "Pod", "solo", 1)
	recent.LastTimestamp = metav1.NewTime(newest)
	recent.Message = "the recent one"

	s := service(
		podWith("solo", "p1", corev1.PodRunning, []corev1.ContainerStatus{running("app")}),
		// Named so the API lists the newest first — the order in which a
		// "last one wins" grouping keeps the older occurrence.
		recent, old,
	)

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(data.WarningEvents) != 1 {
		t.Fatalf("events = %+v, want one group", data.WarningEvents)
	}
	summary := data.WarningEvents[0]
	if !summary.LastTimestamp.Equal(newest) {
		t.Errorf("LastTimestamp = %v, want the newest occurrence %v", summary.LastTimestamp, newest)
	}
	if summary.Message != "the recent one" {
		t.Errorf("Message = %q, want the newest occurrence's message", summary.Message)
	}
}

// settledPod is a healthy, running pod whose container thrashed once and has
// been fine ever since — the shape the window exists to stop reporting.
func settledPod(name string, terminatedAgo time.Duration) *corev1.Pod {
	status := running("app")
	status.RestartCount = 21
	status.LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{
			Reason: "OOMKilled", ExitCode: 137,
			FinishedAt: metav1.NewTime(time.Now().Add(-terminatedAgo)),
		},
	}
	return podWith(name, "p1", corev1.PodRunning, []corev1.ContainerStatus{status})
}

// The analysis is a pure function of the Data, so the instant the window
// opened has to travel on it — recomputing a cutoff in the findings layer
// would measure one gather against two different moments.
func TestGatherRecordsTheWindowOnTheData(t *testing.T) {
	s := service(settledPod("solo", time.Minute))
	s.MaxAge = time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	want := time.Now().Add(-time.Hour)
	if data.Since.Sub(want) > time.Minute || want.Sub(data.Since) > time.Minute {
		t.Errorf("Since = %v, want about %v", data.Since, want)
	}
}

func TestGatherWithoutAWindowLeavesSinceZero(t *testing.T) {
	s := service(settledPod("solo", time.Minute))
	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !data.Since.IsZero() {
		t.Errorf("Since = %v, want zero when no window is set", data.Since)
	}
}

// Logs are fetched for a container with history to explain. Once that history
// falls outside the window nothing will report it, so the previous-instance
// log tail is both irrelevant and an API call per container to avoid.
func TestGatherSkipsLogsForASettledContainer(t *testing.T) {
	s := service(settledPod("solo", 21*24*time.Hour))
	s.MaxAge = 24 * time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if lines := data.Pods[0].Containers[0].LogLines; len(lines) != 0 {
		t.Errorf("log lines = %v, want none for a container that settled weeks ago", lines)
	}
}

func TestGatherStillReadsLogsForARecentTermination(t *testing.T) {
	s := service(settledPod("solo", 10*time.Minute))
	s.MaxAge = 24 * time.Hour

	data, err := s.Gather(context.Background(), kinds.Pod, "solo", ns)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if lines := data.Pods[0].Containers[0].LogLines; len(lines) == 0 {
		t.Error("no log lines for a container that terminated ten minutes ago")
	}
}

// A sweep analyses the same Data the indexed path does, so it has to stamp
// the window too — otherwise a triage table reports history an indexed run
// of the same resource would not.
func TestSweepRecordsTheWindowOnEveryResource(t *testing.T) {
	s := service(settledPod("solo", time.Minute))
	s.MaxAge = time.Hour

	all, err := s.Sweep(context.Background(), ns)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("Sweep returned nothing to check")
	}
	for _, data := range all {
		if data.Since.IsZero() {
			t.Errorf("%s/%s has no window recorded", data.Kind, data.Name)
		}
	}
}
