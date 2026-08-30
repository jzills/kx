package diagnostics

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// swept indexes a sweep by "Kind/name", which is how the assertions below read.
func swept(t *testing.T, results []Data) map[string]Data {
	t.Helper()
	byKey := make(map[string]Data, len(results))
	for _, data := range results {
		byKey[string(data.Kind)+"/"+data.Name] = data
	}
	return byKey
}

func keys(indexed map[string]Data) string {
	all := make([]string, 0, len(indexed))
	for key := range indexed {
		all = append(all, key)
	}
	sort.Strings(all)
	return strings.Join(all, ", ")
}

func podNames(data Data) []string {
	names := make([]string, 0, len(data.Pods))
	for _, pod := range data.Pods {
		names = append(names, pod.Name)
	}
	sort.Strings(names)
	return names
}

func mustSweep(t *testing.T, s Service) map[string]Data {
	t.Helper()
	results, err := s.Sweep(context.Background(), ns)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	return swept(t, results)
}

// metaIn places an object outside the default namespace. The cluster-wide
// sweeps below all turn on a namesake pair, which needs both halves.
func metaIn(namespace, name string, uid types.UID) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace, UID: uid}
}

// sweptCluster indexes by "namespace/Kind/name". swept's key cannot serve a
// cluster-wide sweep: names repeat across namespaces, which is the whole point
// of the rows below.
func sweptCluster(t *testing.T, s Service) map[string]Data {
	t.Helper()
	results, err := s.Sweep(context.Background(), "")
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	byKey := make(map[string]Data, len(results))
	for _, data := range results {
		byKey[data.Namespace+"/"+string(data.Kind)+"/"+data.Name] = data
	}
	return byKey
}

// An empty namespace sweeps the whole cluster. Each row then has to carry the
// namespace it actually came from: labelling them all from the argument would
// stamp every row with the empty string, and the caller can no longer tell one
// web-abc from another.
func TestSweepAcrossAllNamespacesLabelsEachRow(t *testing.T) {
	cluster := service(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "staging", UID: "d2",
		}},
	)

	results, err := cluster.Sweep(context.Background(), "")
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	indexed := swept(t, results)
	if len(indexed) != 2 {
		t.Fatalf("cluster-wide sweep found %s, want both deployments", keys(indexed))
	}
	if got := indexed["Deployment/web"].Namespace; got != ns {
		t.Errorf("web namespace = %q, want %q", got, ns)
	}
	if got := indexed["Deployment/api"].Namespace; got != "staging" {
		t.Errorf("api namespace = %q, want staging", got)
	}

	// A scoped sweep still sees only its own namespace.
	scoped, err := cluster.Sweep(context.Background(), "staging")
	if err != nil {
		t.Fatalf("Sweep(staging): %v", err)
	}
	if only := swept(t, scoped); len(only) != 1 || only["Deployment/api"].Namespace != "staging" {
		t.Errorf("scoped sweep = %s, want only Deployment/api", keys(only))
	}
}

// A Deployment claims its pods through the ReplicaSets it owns, so they must
// not also surface as unowned pods.
func TestSweepDeploymentClaimsItsPods(t *testing.T) {
	indexed := mustSweep(t, service(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", corev1.PodRunning, nil, owner("rs1")),
	))

	if _, ok := indexed["Deployment/web"]; !ok {
		t.Fatalf("Deployment missing from sweep: %s", keys(indexed))
	}
	if got := podNames(indexed["Deployment/web"]); len(got) != 1 || got[0] != "web-abc-1" {
		t.Errorf("deployment pods = %v, want [web-abc-1]", got)
	}
	if _, ok := indexed["Pod/web-abc-1"]; ok {
		t.Errorf("an owned pod also appeared as its own row: %s", keys(indexed))
	}
}

func TestSweepStatefulSetAndDaemonSetClaimTheirPods(t *testing.T) {
	indexed := mustSweep(t, service(
		&appsv1.StatefulSet{ObjectMeta: meta("db", "s1")},
		podWith("db-0", "p1", corev1.PodRunning, nil, owner("s1")),
		&appsv1.DaemonSet{ObjectMeta: meta("agent", "ds1")},
		podWith("agent-xyz", "p2", corev1.PodRunning, nil, owner("ds1")),
	))

	if got := podNames(indexed["StatefulSet/db"]); len(got) != 1 || got[0] != "db-0" {
		t.Errorf("statefulset pods = %v", got)
	}
	if got := podNames(indexed["DaemonSet/agent"]); len(got) != 1 || got[0] != "agent-xyz" {
		t.Errorf("daemonset pods = %v", got)
	}
	for _, unwanted := range []string{"Pod/db-0", "Pod/agent-xyz"} {
		if _, ok := indexed[unwanted]; ok {
			t.Errorf("%s leaked into the orphan pass", unwanted)
		}
	}
}

// A CronJob-owned Job belongs under its CronJob. Reporting it standalone as
// well would show the same failure twice under two different names.
func TestSweepCronJobOwnedJobIsNotReportedStandalone(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: meta("nightly-1", "j1", owner("c1"))}
	job.CreationTimestamp = metav1.NewTime(time.Now())

	indexed := mustSweep(t, service(
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		job,
		podWith("nightly-1-abc", "p1", corev1.PodSucceeded, nil, owner("j1")),
	))

	if _, ok := indexed["CronJob/nightly"]; !ok {
		t.Fatalf("CronJob missing: %s", keys(indexed))
	}
	if _, ok := indexed["Job/nightly-1"]; ok {
		t.Errorf("a CronJob-owned Job was also reported standalone: %s", keys(indexed))
	}
	if got := podNames(indexed["CronJob/nightly"]); len(got) != 1 || got[0] != "nightly-1-abc" {
		t.Errorf("cronjob pods = %v, want the most recent run's", got)
	}
}

// The CronJob row shows only the latest run's pods, but every owned run's pods
// must still be claimed — otherwise an old run's pods reappear at the bottom of
// the triage table as unexplained orphans.
func TestSweepClaimsPodsOfEveryCronJobRunNotJustTheLatest(t *testing.T) {
	old := &batchv1.Job{ObjectMeta: meta("nightly-1", "j1", owner("c1"))}
	old.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Hour))
	recent := &batchv1.Job{ObjectMeta: meta("nightly-2", "j2", owner("c1"))}
	recent.CreationTimestamp = metav1.NewTime(time.Now())

	indexed := mustSweep(t, service(
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		old, recent,
		podWith("nightly-1-old", "p1", corev1.PodFailed, nil, owner("j1")),
		podWith("nightly-2-new", "p2", corev1.PodSucceeded, nil, owner("j2")),
	))

	if _, ok := indexed["Pod/nightly-1-old"]; ok {
		t.Errorf("an old run's pod leaked into the orphan pass: %s", keys(indexed))
	}
	// Only the latest run's pods are shown on the row itself.
	if got := podNames(indexed["CronJob/nightly"]); len(got) != 1 || got[0] != "nightly-2-new" {
		t.Errorf("cronjob pods = %v, want only the most recent run's", got)
	}
}

func TestSweepStandaloneJobIsReported(t *testing.T) {
	indexed := mustSweep(t, service(
		&batchv1.Job{
			ObjectMeta: meta("import", "j1"),
			Status:     batchv1.JobStatus{Failed: 1},
		},
		podWith("import-abc", "p1", corev1.PodFailed, nil, owner("j1")),
	))

	data, ok := indexed["Job/import"]
	if !ok {
		t.Fatalf("standalone Job missing: %s", keys(indexed))
	}
	if data.Job == nil {
		t.Error("standalone Job has no job health attached")
	}
	if _, ok := indexed["Pod/import-abc"]; ok {
		t.Error("a Job's pod leaked into the orphan pass")
	}
}

// A Service matches pods by label, not ownership. Those pods are owned (or
// genuinely unowned) elsewhere, so a Service must not claim them out of the
// orphan pass — otherwise a bare pod behind a Service would vanish from triage.
func TestSweepServiceMatchesPodsWithoutClaimingThem(t *testing.T) {
	bare := podWith("solo", "p1", corev1.PodRunning, nil)
	bare.Labels = map[string]string{"app": "api"}

	indexed := mustSweep(t, service(
		&corev1.Service{
			ObjectMeta: meta("api", "s1"),
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
		},
		bare,
	))

	if got := podNames(indexed["Service/api"]); len(got) != 1 || got[0] != "solo" {
		t.Errorf("service pods = %v, want [solo]", got)
	}
	if _, ok := indexed["Pod/solo"]; !ok {
		t.Errorf("a Service claimed a bare pod out of the orphan pass: %s", keys(indexed))
	}
}

// A selectorless Service matches nothing rather than everything, which is what
// an unguarded label match would do.
func TestSweepSelectorlessServiceMatchesNoPods(t *testing.T) {
	labelled := podWith("solo", "p1", corev1.PodRunning, nil)
	labelled.Labels = map[string]string{"app": "api"}

	indexed := mustSweep(t, service(
		&corev1.Service{ObjectMeta: meta("headless", "s1")},
		labelled,
	))
	if got := podNames(indexed["Service/headless"]); len(got) != 0 {
		t.Errorf("selectorless service matched %v, want nothing", got)
	}
}

func TestSweepReportsPVCs(t *testing.T) {
	indexed := mustSweep(t, service(&corev1.PersistentVolumeClaim{
		ObjectMeta: meta("data", "v1"),
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimLost},
	}))

	data, ok := indexed["PersistentVolumeClaim/data"]
	if !ok {
		t.Fatalf("PVC missing: %s", keys(indexed))
	}
	if data.PVC == nil || data.PVC.Phase != "Lost" {
		t.Errorf("pvc = %+v, want phase Lost", data.PVC)
	}
}

func TestSweepReportsIngressMissingBackend(t *testing.T) {
	indexed := mustSweep(t, service(
		&networkingv1.Ingress{
			ObjectMeta: meta("web", "i1"),
			Spec: networkingv1.IngressSpec{
				DefaultBackend: &networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "api"},
				},
			},
		},
	))

	data, ok := indexed["Ingress/web"]
	if !ok {
		t.Fatalf("Ingress missing: %s", keys(indexed))
	}
	if data.Ingress == nil || len(data.Ingress.MissingBackends) != 1 ||
		data.Ingress.MissingBackends[0] != "api" {
		t.Errorf("ingress = %+v, want MissingBackends [api]", data.Ingress)
	}
}

func TestSweepReportsIngressWithExistingBackend(t *testing.T) {
	indexed := mustSweep(t, service(
		&networkingv1.Ingress{
			ObjectMeta: meta("web", "i1"),
			Spec: networkingv1.IngressSpec{
				DefaultBackend: &networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "api"},
				},
			},
		},
		&corev1.Service{ObjectMeta: meta("api", "s1")},
	))

	data, ok := indexed["Ingress/web"]
	if !ok {
		t.Fatalf("Ingress missing: %s", keys(indexed))
	}
	if data.Ingress == nil || len(data.Ingress.MissingBackends) != 0 {
		t.Errorf("ingress = %+v, want no missing backends", data.Ingress)
	}
}

// A cluster-wide sweep must not let a same-named Service in a different
// namespace mask a genuinely missing backend — the same regression
// usageKey/endpointsKey guard against elsewhere in this file.
func TestSweepIngressBackendLookupIsNamespaceQualified(t *testing.T) {
	s := service(
		&networkingv1.Ingress{
			ObjectMeta: meta("web", "i1"), // namespace "prod", via meta()
			Spec: networkingv1.IngressSpec{
				DefaultBackend: &networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "api"},
				},
			},
		},
		&corev1.Service{ObjectMeta: metaIn("other-ns", "api", "s1")},
	)
	results, err := s.Sweep(context.Background(), "")
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	indexed := swept(t, results)
	data, ok := indexed["Ingress/web"]
	if !ok {
		t.Fatalf("Ingress missing: %s", keys(indexed))
	}
	if data.Ingress == nil || len(data.Ingress.MissingBackends) != 1 ||
		data.Ingress.MissingBackends[0] != "api" {
		t.Errorf("ingress = %+v, want the cross-namespace Service to NOT satisfy the backend", data.Ingress)
	}
}

// Anything nothing else claimed is a row of its own, so a bare pod is never
// silently absent from triage.
func TestSweepReportsUnclaimedPods(t *testing.T) {
	indexed := mustSweep(t, service(
		podWith("solo", "p1", corev1.PodFailed, []corev1.ContainerStatus{
			terminated("app", "Error", 1),
		}),
	))

	data, ok := indexed["Pod/solo"]
	if !ok {
		t.Fatalf("bare pod missing: %s", keys(indexed))
	}
	if len(data.Pods) != 1 || data.Pods[0].Phase != "Failed" {
		t.Errorf("pod diagnostic = %+v", data.Pods)
	}
}

func TestSweepEmptyNamespaceYieldsNothing(t *testing.T) {
	results, err := service().Sweep(context.Background(), ns)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("sweep of an empty namespace = %d rows, want 0", len(results))
	}
}

// The sweep is what feeds the triage table, so every row needs its warning
// events attached — not just the ones a detailed gather would fetch.
func TestSweepAttachesWarningEvents(t *testing.T) {
	indexed := mustSweep(t, service(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", corev1.PodPending, nil, owner("rs1")),
		warning("e1", "FailedScheduling", "Pod", "web-abc-1", 2),
	))

	events := indexed["Deployment/web"].WarningEvents
	if len(events) != 1 || events[0].Reason != "FailedScheduling" {
		t.Fatalf("events = %+v, want one FailedScheduling", events)
	}
	if events[0].Count != 2 {
		t.Errorf("count = %d, want 2", events[0].Count)
	}
}

// warningIn is warning for another namespace. It leaves InvolvedObject.Namespace
// unset exactly as the API routinely does, so the match has to fall back to the
// event's own metadata rather than trusting the reference.
func warningIn(namespace, name, reason, kind, object string) *corev1.Event {
	event := warning(name, reason, kind, object, 1)
	event.Namespace = namespace
	return event
}

// Events are matched on the involved object's name and kind, which identify it
// only within a namespace. A cluster-wide sweep holds every namespace's events
// at once, so an unscoped match hands a healthy resource its namesake's
// warnings — and warnings drive findings, so the row lands in the triage table
// as someone else's failure.
func TestSweepAcrossAllNamespacesKeepsEventsInTheirNamespace(t *testing.T) {
	indexed := sweptCluster(t, service(
		podWith("web-0", "p1", corev1.PodRunning, nil),
		&corev1.Pod{
			ObjectMeta: metaIn("staging", "web-0", "p2"),
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		warning("e1", "ProdUnhealthy", "Pod", "web-0", 1),
		warningIn("staging", "e2", "StagingUnhealthy", "Pod", "web-0"),
	))

	for _, want := range []struct{ namespace, reason string }{
		{ns, "ProdUnhealthy"},
		{"staging", "StagingUnhealthy"},
	} {
		events := indexed[want.namespace+"/Pod/web-0"].WarningEvents
		if len(events) != 1 || events[0].Reason != want.reason {
			t.Errorf("%s events = %+v, want only %s", want.namespace, events, want.reason)
		}
	}
}

// A Service is paired with the Endpoints object of the same name, which is the
// same name in every namespace. Keyed by name alone, two namespaces' Services
// share whichever Endpoints was listed last — and no endpoints is a Critical
// finding, so a broken Service can be scored healthy and drop out of the table
// altogether.
func TestSweepAcrossAllNamespacesPairsEachServiceWithItsOwnEndpoints(t *testing.T) {
	selector := corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}
	indexed := sweptCluster(t, service(
		&corev1.Service{ObjectMeta: meta("web", "s1"), Spec: selector},
		&corev1.Service{ObjectMeta: metaIn("staging", "web", "s2"), Spec: selector},
		&corev1.Endpoints{ObjectMeta: meta("web", "e1")},
		&corev1.Endpoints{
			ObjectMeta: metaIn("staging", "web", "e2"),
			Subsets: []corev1.EndpointSubset{{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
			}},
		},
	))

	if got := indexed[ns+"/Service/web"].Service; got == nil || got.ReadyAddresses != 0 {
		t.Errorf("prod service = %+v, want its own empty endpoints", got)
	}
	if got := indexed["staging/Service/web"].Service; got == nil || got.ReadyAddresses != 1 {
		t.Errorf("staging service = %+v, want its own ready endpoint", got)
	}
}

// A Service selects pods within its own namespace — label sets are not unique
// across a cluster, and app=web means something different in every namespace.
// Unscoped, a cluster-wide sweep hands a Service the foreign pods that happen
// to share its labels, and their findings land on its row.
func TestSweepAcrossAllNamespacesMatchesOnlyPodsInTheServiceNamespace(t *testing.T) {
	labels := map[string]string{"app": "web"}
	local := podWith("web-prod", "p1", corev1.PodRunning, nil)
	local.Labels = labels
	foreign := &corev1.Pod{
		ObjectMeta: metaIn("staging", "web-staging", "p2"),
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	foreign.Labels = labels

	indexed := sweptCluster(t, service(
		&corev1.Service{
			ObjectMeta: meta("web", "s1"),
			Spec:       corev1.ServiceSpec{Selector: labels},
		},
		local, foreign,
	))

	if got := podNames(indexed[ns+"/Service/web"]); len(got) != 1 || got[0] != "web-prod" {
		t.Errorf("service pods = %v, want only [web-prod]", got)
	}
}

// End to end through the analysis layer: the sweep is only useful if its rows
// carry enough for BuildReport to reach a verdict.
func TestSweepRowsProduceVerdicts(t *testing.T) {
	indexed := mustSweep(t, service(
		&appsv1.Deployment{
			ObjectMeta: meta("broken", "d1"),
			Spec:       appsv1.DeploymentSpec{Replicas: i32(2)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
		},
		&appsv1.Deployment{
			ObjectMeta: meta("healthy", "d2"),
			Spec:       appsv1.DeploymentSpec{Replicas: i32(1)},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1,
			},
		},
	))

	if got := BuildReport(indexed["Deployment/broken"]).Verdict; got != Critical {
		t.Errorf("broken verdict = %v, want critical", got)
	}
	if got := BuildReport(indexed["Deployment/healthy"]).Verdict; got != OK {
		t.Errorf("healthy verdict = %v, want healthy", got)
	}
}

func TestOwnedPods(t *testing.T) {
	pods := []corev1.Pod{
		*podWith("a", "p1", corev1.PodRunning, nil, owner("rs1")),
		*podWith("b", "p2", corev1.PodRunning, nil, owner("rs2")),
		*podWith("c", "p3", corev1.PodRunning, nil),
	}
	got := ownedPods(pods, map[types.UID]bool{"rs1": true})
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("ownedPods = %+v, want [a]", got)
	}
	if len(ownedPods(pods, nil)) != 0 {
		t.Error("ownedPods with no owners matched something")
	}
}

func TestSupportedKindsCoversEveryKindSweepEmits(t *testing.T) {
	indexed := mustSweep(t, service(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.StatefulSet{ObjectMeta: meta("db", "s1")},
		&appsv1.DaemonSet{ObjectMeta: meta("agent", "ds1")},
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		&batchv1.Job{ObjectMeta: meta("import", "j1")},
		&corev1.Service{ObjectMeta: meta("api", "sv1")},
		&corev1.PersistentVolumeClaim{ObjectMeta: meta("data", "v1")},
		podWith("solo", "p1", corev1.PodRunning, nil),
	))

	// kx diag <index> refuses a kind outside SupportedKinds, so a kind the sweep
	// can put on screen but the detail view rejects is a dead end for the user.
	for key, data := range indexed {
		if !SupportedKinds.Has(data.Kind) {
			t.Errorf("sweep emitted %s, which SupportedKinds rejects", key)
		}
	}
	if len(indexed) != 8 {
		t.Errorf("swept %d rows (%s), want 8", len(indexed), keys(indexed))
	}
}
