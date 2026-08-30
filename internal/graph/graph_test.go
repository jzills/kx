package graph

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/tree"
)

const ns = "prod"

func meta(name string, uid types.UID, owners ...metav1.OwnerReference) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: name, Namespace: ns, UID: uid, OwnerReferences: owners,
	}
}

func owner(uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{UID: uid}
}

func podWith(name string, uid types.UID, containers []string, owners ...metav1.OwnerReference) *corev1.Pod {
	pod := &corev1.Pod{ObjectMeta: meta(name, uid, owners...)}
	for _, container := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: container})
	}
	return pod
}

func builder(objects ...runtime.Object) Builder {
	return Builder{Client: fake.NewSimpleClientset(objects...)}
}

// flatten renders the tree structurally, so assertions read as the shape a user
// would see rather than as nested field access.
func flatten(node *tree.Node, depth int) []string {
	lines := []string{strings.Repeat("  ", depth) + node.Label}
	for _, child := range node.Children {
		lines = append(lines, flatten(child, depth+1)...)
	}
	return lines
}

func treeOf(t *testing.T, node *tree.Node) string {
	t.Helper()
	return strings.Join(flatten(node, 0), "\n")
}

// A Deployment is a two-hop walk: Deployment → owned ReplicaSets → owned pods.
func TestDeploymentTreeWalksTwoHops(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", []string{"app"}, owner("rs1")),
		podWith("web-abc-2", "p2", []string{"app", "sidecar"}, owner("rs1")),
	)
	node, _, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	want := strings.Join([]string{
		"Deployment/web",
		"  rs/web-abc",
		"    pod/web-abc-1",
		"      container: app",
		"    pod/web-abc-2",
		"      container: app",
		"      container: sidecar",
	}, "\n")
	if got := treeOf(t, node); got != want {
		t.Errorf("tree =\n%s\nwant\n%s", got, want)
	}
}

// Mid-rollout a Deployment owns several ReplicaSets; all must show, or surge
// and old pods vanish from the graph.
func TestDeploymentTreeShowsAllReplicaSets(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-new", "rs1", owner("d1"))},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-old", "rs2", owner("d1"))},
		podWith("web-new-1", "p1", nil, owner("rs1")),
		podWith("web-old-1", "p2", nil, owner("rs2")),
	)
	node, _, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	tree := treeOf(t, node)
	for _, want := range []string{"rs/web-new", "rs/web-old", "pod/web-new-1", "pod/web-old-1"} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree is missing %q:\n%s", want, tree)
		}
	}
}

// A ReplicaSet owned by a different Deployment must not appear.
func TestDeploymentTreeExcludesForeignReplicaSets(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		&appsv1.ReplicaSet{ObjectMeta: meta("other-xyz", "rs2", owner("d2"))},
	)
	node, _, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if strings.Contains(treeOf(t, node), "other-xyz") {
		t.Errorf("tree includes a ReplicaSet owned by another Deployment:\n%s", treeOf(t, node))
	}
}

func TestStatefulSetAndDaemonSetOwnPodsDirectly(t *testing.T) {
	cases := []struct {
		kind   kinds.Kind
		object runtime.Object
	}{
		{kinds.StatefulSet, &appsv1.StatefulSet{ObjectMeta: meta("db", "s1")}},
		{kinds.DaemonSet, &appsv1.DaemonSet{ObjectMeta: meta("db", "s1")}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			b := builder(tc.object, podWith("db-0", "p1", []string{"app"}, owner("s1")))
			node, _, err := b.BuildResource(context.Background(), tc.kind, "db", ns, false)
			if err != nil {
				t.Fatalf("BuildResource: %v", err)
			}
			want := string(tc.kind) + "/db\n  pod/db-0\n    container: app"
			if got := treeOf(t, node); got != want {
				t.Errorf("tree =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestCronJobTreeWalksJobsThenPods(t *testing.T) {
	b := builder(
		&batchv1.CronJob{ObjectMeta: meta("nightly", "c1")},
		&batchv1.Job{ObjectMeta: meta("nightly-1", "j1", owner("c1"))},
		podWith("nightly-1-abc", "p1", []string{"run"}, owner("j1")),
	)
	node, _, err := b.BuildResource(context.Background(), kinds.CronJob, "nightly", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	want := "CronJob/nightly\n  job/nightly-1\n    pod/nightly-1-abc\n      container: run"
	if got := treeOf(t, node); got != want {
		t.Errorf("tree =\n%s\nwant\n%s", got, want)
	}
}

// A Service selects pods by label, not by ownership.
func TestServiceTreeUsesSelector(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: meta("web", "sv1"),
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
	}
	selected := podWith("web-1", "p1", []string{"app"})
	selected.Labels = map[string]string{"app": "web"}

	b := builder(service, selected)
	node, _, err := b.BuildResource(context.Background(), kinds.Service, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if got := treeOf(t, node); !strings.Contains(got, "pod/web-1") {
		t.Errorf("tree =\n%s\nwant the selected pod", got)
	}
}

// A headless or externalName Service selects nothing; say so rather than
// rendering an empty branch.
func TestServiceWithoutSelectorSaysSo(t *testing.T) {
	b := builder(&corev1.Service{ObjectMeta: meta("web", "sv1")})
	node, _, err := b.BuildResource(context.Background(), kinds.Service, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if got := treeOf(t, node); !strings.Contains(got, "(no selector)") {
		t.Errorf("tree =\n%s\nwant a (no selector) note", got)
	}
}

func TestServiceWithNoMatchingPodsSaysSo(t *testing.T) {
	b := builder(&corev1.Service{
		ObjectMeta: meta("web", "sv1"),
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
	})
	node, _, err := b.BuildResource(context.Background(), kinds.Service, "web", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if got := treeOf(t, node); !strings.Contains(got, "(no matching pods)") {
		t.Errorf("tree =\n%s\nwant a (no matching pods) note", got)
	}
}

func TestPodTreeListsContainers(t *testing.T) {
	b := builder(podWith("nginx", "p1", []string{"app", "sidecar"}))
	node, _, err := b.BuildResource(context.Background(), kinds.Pod, "nginx", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	want := "Pod/nginx\n  container: app\n  container: sidecar"
	if got := treeOf(t, node); got != want {
		t.Errorf("tree =\n%s\nwant\n%s", got, want)
	}
}

func TestUnsupportedKindSaysSo(t *testing.T) {
	b := builder()
	node, _, err := b.BuildResource(context.Background(), kinds.ConfigMap, "cm", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if got := treeOf(t, node); !strings.Contains(got, "no ownership graph for ConfigMap") {
		t.Errorf("tree =\n%s\nwant an unsupported-kind note", got)
	}
}

// The indexed root numbers 1, and its descendants follow in walk order — the
// order later commands will resolve those indexes against.
func TestIndexedResourceTreeNumbersInWalkOrder(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", nil, owner("rs1")),
	)
	node, resources, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, true)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if node.Index != 1 {
		t.Errorf("root index = %d, want 1", node.Index)
	}
	want := []Resource{
		{Name: "web", Kind: kinds.Deployment, Namespace: ns},
		{Name: "web-abc", Kind: kinds.ReplicaSet, Namespace: ns},
		{Name: "web-abc-1", Kind: kinds.Pod, Namespace: ns},
	}
	if len(resources) != len(want) {
		t.Fatalf("resources = %v, want %v", resources, want)
	}
	for i := range want {
		if resources[i] != want[i] {
			t.Errorf("resources[%d] = %v, want %v", i, resources[i], want[i])
		}
	}
}

// Containers are not indexable — they aren't resources kubectl can act on.
func TestContainersAreNotIndexed(t *testing.T) {
	b := builder(podWith("nginx", "p1", []string{"app"}))
	_, resources, err := b.BuildResource(context.Background(), kinds.Pod, "nginx", ns, true)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("resources = %v, want only the pod itself", resources)
	}
}

func TestUnindexedTreeRecordsNothing(t *testing.T) {
	b := builder(podWith("nginx", "p1", []string{"app"}))
	node, resources, err := b.BuildResource(context.Background(), kinds.Pod, "nginx", ns, false)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("resources = %v, want none", resources)
	}
	if node.Index != 0 {
		t.Errorf("root index = %d, want 0 when unindexed", node.Index)
	}
}

// Every node the walk builds carries the kind and name beside its label, or
// --json has to parse "rs/web-7d8f" back apart to recover what the walk
// already knew. Containers are the one exception: they are not resources, so
// they carry a name and no kind.
func TestTreeNodesCarryKindAndName(t *testing.T) {
	b := builder(
		&appsv1.Deployment{ObjectMeta: meta("web", "d1")},
		&appsv1.ReplicaSet{ObjectMeta: meta("web-abc", "rs1", owner("d1"))},
		podWith("web-abc-1", "p1", []string{"app"}, owner("rs1")),
	)
	root, _, err := b.BuildResource(context.Background(), kinds.Deployment, "web", ns, true)
	if err != nil {
		t.Fatalf("BuildResource: %v", err)
	}

	seen := 0
	var walk func(*tree.Node)
	walk = func(node *tree.Node) {
		seen++
		if strings.HasPrefix(node.Label, "container: ") {
			if node.Kind != "" {
				t.Errorf("container %q carries kind %q, want none — it is not a resource",
					node.Label, node.Kind)
			}
			if node.Name == "" {
				t.Errorf("container %q carries no name", node.Label)
			}
		} else {
			if node.Kind == "" || node.Name == "" {
				t.Errorf("node %q carries kind=%q name=%q, want both",
					node.Label, node.Kind, node.Name)
			}
			// The fields and the label must describe the same resource, or
			// --json and the drawn tree would disagree about what a row is.
			if !strings.HasSuffix(node.Label, "/"+node.Name) {
				t.Errorf("node %q disagrees with its own Name %q", node.Label, node.Name)
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	if seen < 4 {
		t.Fatalf("walked %d nodes, want deployment, replicaset, pod and container", seen)
	}
}
