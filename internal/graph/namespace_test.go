package graph

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jzills/kx/internal/kinds"
)

func namespaceTree(t *testing.T, b Builder, indexed bool) (string, []Resource) {
	t.Helper()
	node, resources, err := b.BuildNamespace(context.Background(), ns, indexed)
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
	node, resources, err := b.BuildNamespace(context.Background(), ns, true)
	if err != nil {
		t.Fatalf("BuildNamespace: %v", err)
	}
	if node.Index != 0 {
		t.Errorf("Namespace root index = %d, want it unindexed", node.Index)
	}
	want := []Resource{
		{Name: "web", Kind: kinds.Deployment},
		{Name: "web-abc", Kind: kinds.ReplicaSet},
		{Name: "web-abc-1", Kind: kinds.Pod},
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
