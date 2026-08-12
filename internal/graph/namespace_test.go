package graph

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jzills/kx/internal/kinds"
)

func namespaceTree(t *testing.T, b Builder, indexed bool) (string, []Resource) {
	t.Helper()
	node, resources, err := b.BuildNamespace(context.Background(), ns, indexed, 0)
	if err != nil {
		t.Fatalf("BuildNamespace: %v", err)
	}
	return treeOf(t, node), resources
}

func TestNamespaceForestNestsOwnedResources(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", []string{"app"}, owner("rs1")),
	)
	tree, _ := namespaceTree(t, b, false)
	want := strings.Join([]string{
		"Namespace/prod",
		"  Deployment/web",
		"    rs/web-abc",
		"      pod/web-abc-1",
		"        container: app",
	}, "\n")
	if tree != want {
		t.Errorf("tree =\n%s\nwant\n%s", tree, want)
	}
}

// A pod whose owner isn't in the namespace is still shown, as a root — nothing
// may be hidden.
func TestNamespaceForestShowsOrphansAsRoots(t *testing.T) {
	b := builder(
		podWith("bare", "p1", nil),
		podWith("adopted", "p2", nil, owner("missing-owner")),
	)
	tree, _ := namespaceTree(t, b, false)
	for _, want := range []string{"pod/bare", "pod/adopted"} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree is missing orphan %q:\n%s", want, tree)
		}
	}
}

// Every pod appears exactly once: under its owner when it has one in the
// namespace, as a root when it doesn't.
func TestNamespaceForestShowsEachPodOnce(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", nil, owner("rs1")),
		podWith("bare", "p2", nil),
	)
	tree, _ := namespaceTree(t, b, false)
	if count := strings.Count(tree, "pod/web-abc-1"); count != 1 {
		t.Errorf("owned pod appears %d times, want 1:\n%s", count, tree)
	}
	if count := strings.Count(tree, "pod/bare"); count != 1 {
		t.Errorf("orphan pod appears %d times, want 1:\n%s", count, tree)
	}
}

// An owned ReplicaSet is not also a root.
func TestNamespaceForestDoesNotDuplicateOwnedControllers(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
	)
	tree, _ := namespaceTree(t, b, false)
	if count := strings.Count(tree, "rs/web-abc"); count != 1 {
		t.Errorf("owned ReplicaSet appears %d times, want 1:\n%s", count, tree)
	}
}

// Roots are ordered by kind then name, so the same namespace always renders the
// same way.
func TestNamespaceForestRootOrderIsStable(t *testing.T) {
	b := builder(
		podWith("zz-bare", "p1", nil),
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		&appsv1.StatefulSet{ObjectMeta: meta("db", "s1")},
		&appsv1.Deployment{ObjectMeta: meta("web-b", "d2")},
		&appsv1.Deployment{ObjectMeta: meta("web-a", "d1")},
		&appsv1.DaemonSet{ObjectMeta: meta("agent", "ds1")},
	)
	first, _ := namespaceTree(t, b, false)
	want := strings.Join([]string{
		"Namespace/prod",
		"  Deployment/web-a",
		"  Deployment/web-b",
		"  StatefulSet/db",
		"  DaemonSet/agent",
		"  CronJob/nightly",
		"  pod/zz-bare",
	}, "\n")
	if first != want {
		t.Errorf("tree =\n%s\nwant\n%s", first, want)
	}

	// Repeated builds must not reorder: the walk indexes children through Go
	// maps, and unstable ordering would renumber indexes between runs.
	for attempt := 0; attempt < 20; attempt++ {
		again, _ := namespaceTree(t, b, false)
		if again != first {
			t.Fatalf("attempt %d reordered the forest:\n%s\nfirst:\n%s", attempt, again, first)
		}
	}
}

func TestEmptyNamespaceSaysSo(t *testing.T) {
	tree, resources := namespaceTree(t, builder(), false)
	if !strings.Contains(tree, "(no workloads)") {
		t.Errorf("tree =\n%s\nwant a (no workloads) note", tree)
	}
	if len(resources) != 0 {
		t.Errorf("resources = %v, want none", resources)
	}
}

// Unlike a single-resource tree, the Namespace root is not indexed — children
// number from 1.
func TestNamespaceForestNumbersFromOne(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", nil, owner("rs1")),
	)
	node, resources, err := b.BuildNamespace(context.Background(), ns, true, 0)
	if err != nil {
		t.Fatalf("BuildNamespace: %v", err)
	}
	if node.Index != 0 {
		t.Errorf("Namespace root index = %d, want it unindexed", node.Index)
	}
	want := []Resource{
		{Name: "web", Kind: kinds.Deployment, Namespace: ns},
		{Name: "web-abc", Kind: kinds.ReplicaSet, Namespace: ns},
		{Name: "web-abc-1", Kind: kinds.Pod, Namespace: ns},
	}
	for i := range want {
		if resources[i] != want[i] {
			t.Errorf("resources[%d] = %v, want %v", i, resources[i], want[i])
		}
	}
	if node.Children[0].Index != 1 {
		t.Errorf("first child index = %d, want 1", node.Children[0].Index)
	}
}

// CronJob health and pods are scoped to the latest run, not the full retained
// history.
func TestMostRecentJob(t *testing.T) {
	older := batchv1.Job{ObjectMeta: meta("nightly-1", "j1", owner("c1"))}
	older.CreationTimestamp = metav1.Now()
	newer := batchv1.Job{ObjectMeta: meta("nightly-2", "j2", owner("c1"))}
	newer.CreationTimestamp = metav1.NewTime(older.CreationTimestamp.Add(60e9))
	foreign := batchv1.Job{ObjectMeta: meta("other-1", "j3", owner("c2"))}
	foreign.CreationTimestamp = metav1.NewTime(newer.CreationTimestamp.Add(60e9))

	recent := MostRecentJob("c1", []batchv1.Job{older, newer, foreign})
	if recent == nil || recent.Name != "nightly-2" {
		t.Errorf("MostRecentJob = %v, want nightly-2", recent)
	}

	if MostRecentJob("never-ran", []batchv1.Job{older}) != nil {
		t.Error("MostRecentJob on an unrun CronJob returned a job")
	}
}

// Namespaces sorts, so an -A tree sweep walks a stable order rather than
// whatever order the fake (or real) API server happens to return objects in.
func TestNamespacesListsSorted(t *testing.T) {
	b := builder(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
	)
	names, err := b.Namespaces(context.Background())
	if err != nil {
		t.Fatalf("Namespaces: %v", err)
	}
	want := []string{"default", "kube-system", "prod"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

// Every walked resource records the namespace it was found in, so a forest
// spanning namespaces can still resolve an index to one place. Within a single
// namespace it is the same value on every entry; across an -A sweep it is the
// only thing telling two same-named workloads apart.
func TestNamespaceForestRecordsTheNamespace(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
	)

	_, resources := namespaceTree(t, b, true)

	if len(resources) == 0 {
		t.Fatal("walk recorded no resources")
	}
	for _, resource := range resources {
		if resource.Namespace != ns {
			t.Errorf("resource %s/%s recorded namespace %q, want %q",
				resource.Kind, resource.Name, resource.Namespace, ns)
		}
	}
}

// A single-resource tree records it too — the sweep is not the only caller, and
// an entry that carried none would fall back to the listing's namespace, which
// for an -A tree is empty.
func TestResourceTreeRecordsTheNamespace(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
	)

	_, resources, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, true)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	if len(resources) == 0 {
		t.Fatal("walk recorded no resources")
	}
	for _, resource := range resources {
		if resource.Namespace != ns {
			t.Errorf("resource %s/%s recorded namespace %q, want %q",
				resource.Kind, resource.Name, resource.Namespace, ns)
		}
	}
}
