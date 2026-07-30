package graph

import (
	"context"
	"sort"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jzills/kx/internal/kinds"
)

func resolve(t *testing.T, b Builder, kind kinds.Kind, name string) []string {
	t.Helper()
	pods, err := b.ResolveWorkloadPods(context.Background(), kind, name, ns)
	if err != nil {
		t.Fatalf("ResolveWorkloadPods(%s/%s): %v", kind, name, err)
	}
	names := make([]string, 0, len(pods))
	for _, pod := range pods {
		names = append(names, pod.Name)
	}
	sort.Strings(names)
	return names
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A Deployment reaches its pods through the ReplicaSets it owns, including the
// old one mid-rollout — surge pods are part of the workload's health.
func TestResolveDeploymentPodsAcrossReplicaSets(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-new", "rs1", owner("d1"))},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-old", "rs2", owner("d1"))},
		&appsv1.ReplicaSet{ObjectMeta: meta("other", "rs3", owner("d9"))},
		podWith("web-new-1", "p1", nil, owner("rs1")),
		podWith("web-old-1", "p2", nil, owner("rs2")),
		podWith("foreign-1", "p3", nil, owner("rs3")),
	)
	if got := resolve(t, b, kinds.Deployment, "web"); !equal(got, []string{"web-new-1", "web-old-1"}) {
		t.Errorf("pods = %v, want both replica sets' pods and no others", got)
	}
}

func TestResolveDirectlyOwnedPods(t *testing.T) {
	b := builder(
		&appsv1.StatefulSet{ObjectMeta: meta("db", "s1")},
		podWith("db-0", "p1", nil, owner("s1")),
		&appsv1.DaemonSet{ObjectMeta: meta("agent", "ds1")},
		podWith("agent-a", "p2", nil, owner("ds1")),
		&batchv1.Job{ObjectMeta: meta("import", "j1")},
		podWith("import-a", "p3", nil, owner("j1")),
	)
	for _, tc := range []struct {
		kind kinds.Kind
		name string
		want []string
	}{
		{kinds.StatefulSet, "db", []string{"db-0"}},
		{kinds.DaemonSet, "agent", []string{"agent-a"}},
		{kinds.Job, "import", []string{"import-a"}},
	} {
		if got := resolve(t, b, tc.kind, tc.name); !equal(got, tc.want) {
			t.Errorf("%s/%s pods = %v, want %v", tc.kind, tc.name, got, tc.want)
		}
	}
}

func TestResolvePodIsItself(t *testing.T) {
	b := builder(podWith("solo", "p1", []string{"app"}))
	if got := resolve(t, b, kinds.Pod, "solo"); !equal(got, []string{"solo"}) {
		t.Errorf("pods = %v, want [solo]", got)
	}
}

// A Service selects by label rather than ownership, so its pods need not be
// owned by anything.
func TestResolveServicePodsBySelector(t *testing.T) {
	matching := podWith("api-1", "p1", nil)
	matching.Labels = map[string]string{"app": "api"}
	other := podWith("web-1", "p2", nil)
	other.Labels = map[string]string{"app": "web"}

	b := builder(
		&corev1.Service{
			ObjectMeta: meta("api", "s1"),
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
		},
		matching, other,
	)
	if got := resolve(t, b, kinds.Service, "api"); !equal(got, []string{"api-1"}) {
		t.Errorf("pods = %v, want [api-1]", got)
	}
}

// A selectorless Service selects nothing. Returning every pod instead would
// make a headless Service look like the whole namespace.
func TestResolveSelectorlessServiceHasNoPods(t *testing.T) {
	b := builder(
		&corev1.Service{ObjectMeta: meta("headless", "s1")},
		podWith("solo", "p1", nil),
	)
	if got := resolve(t, b, kinds.Service, "headless"); len(got) != 0 {
		t.Errorf("pods = %v, want none", got)
	}
}

// A CronJob's pods are the latest run's, not every retained run's — otherwise a
// long-lived CronJob reports on pods from days ago.
func TestResolveCronJobPodsScopeToTheLatestRun(t *testing.T) {
	old := &batchv1.Job{ObjectMeta: meta("nightly-1", "j1", owner("c1"))}
	old.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Hour))
	recent := &batchv1.Job{ObjectMeta: meta("nightly-2", "j2", owner("c1"))}
	recent.CreationTimestamp = metav1.NewTime(time.Now())

	b := builder(
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		old, recent,
		podWith("nightly-1-a", "p1", nil, owner("j1")),
		podWith("nightly-2-a", "p2", nil, owner("j2")),
	)
	if got := resolve(t, b, kinds.CronJob, "nightly"); !equal(got, []string{"nightly-2-a"}) {
		t.Errorf("pods = %v, want only the most recent run's", got)
	}
}

func TestResolveCronJobThatHasNeverRun(t *testing.T) {
	b := builder(&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")})
	if got := resolve(t, b, kinds.CronJob, "nightly"); len(got) != 0 {
		t.Errorf("pods = %v, want none", got)
	}
}

// A kind with no pod relationship resolves to nothing rather than erroring, so
// a diagnostic on it still reports whatever else it gathered.
func TestResolveUnsupportedKindYieldsNoPods(t *testing.T) {
	b := builder()
	if got := resolve(t, b, kinds.ConfigMap, "settings"); len(got) != 0 {
		t.Errorf("pods = %v, want none", got)
	}
}

func TestResolveMissingWorkloadIsAnError(t *testing.T) {
	b := builder()
	if _, err := b.ResolveWorkloadPods(
		context.Background(), kinds.Deployment, "nope", ns); err == nil {
		t.Error("resolving a missing Deployment returned no error")
	}
}

func TestMatchesSelector(t *testing.T) {
	pod := *podWith("p", "p1", nil)
	pod.Labels = map[string]string{"app": "api", "tier": "backend"}

	// Every entry in the selector must match; the pod may carry extras.
	if !MatchesSelector(pod, map[string]string{"app": "api"}) {
		t.Error("a subset selector did not match")
	}
	if MatchesSelector(pod, map[string]string{"app": "api", "env": "prod"}) {
		t.Error("a selector with an unmatched key still matched")
	}
	if MatchesSelector(pod, map[string]string{"app": "web"}) {
		t.Error("a selector with a wrong value still matched")
	}
}
