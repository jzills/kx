package cli

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
)

func treeFixture() graph.Builder {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "web", Namespace: "prod", UID: types.UID("d1"),
	}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "web-abc", Namespace: "prod", UID: types.UID("rs1"),
		OwnerReferences: []metav1.OwnerReference{{UID: types.UID("d1")}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "web-abc-1", Namespace: "prod", UID: types.UID("p1"),
		OwnerReferences: []metav1.OwnerReference{{UID: types.UID("rs1")}},
	}}
	return graph.Builder{Client: fake.NewSimpleClientset(deployment, replicaSet, pod)}
}

func TestTreeSavesIndexedNodesInWalkOrder(t *testing.T) {
	states := &fakeState{}
	command := TreeCommand{
		Builder: treeFixture(),
		State:   workload("web", kinds.Deployment),
		Save:    states.Save,
	}
	if _, err := command.Execute(context.Background(), 1, true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(states.saved))
	}

	// Index order is walk order, so `kx describe 3` reaches the pod the tree
	// numbered 3.
	want := []string{"web", "web-abc", "web-abc-1"}
	got := states.saved[0].Names()
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if kind, _ := states.saved[0].Resources.Kind("web-abc"); kind != kinds.ReplicaSet {
		t.Errorf("kind = %q, want ReplicaSet", kind)
	}
}

// A tree entry has no Query: it wasn't produced by `kx get`, so there is
// nothing to re-run if it goes stale.
func TestTreeSavesWithoutQuery(t *testing.T) {
	states := &fakeState{}
	command := TreeCommand{
		Builder: treeFixture(), State: workload("web", kinds.Deployment), Save: states.Save,
	}
	if _, err := command.Execute(context.Background(), 1, true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if states.saved[0].Query != nil {
		t.Errorf("Query = %+v, want nil", states.saved[0].Query)
	}
}

// Without --index the tree is display-only and must not disturb the listing the
// user is working through.
func TestUnindexedTreeSavesNothing(t *testing.T) {
	states := &fakeState{}
	command := TreeCommand{
		Builder: treeFixture(), State: workload("web", kinds.Deployment), Save: states.Save,
	}
	if _, err := command.Execute(context.Background(), 1, false); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 0 {
		t.Errorf("saved %d entries, want 0", len(states.saved))
	}
}

// A Namespace row graphs that namespace itself — its own name, not the
// namespace the `kx get ns` ran in.
func TestTreeOnNamespaceIndexGraphsThatNamespace(t *testing.T) {
	states := &fakeState{}
	command := TreeCommand{
		Builder: treeFixture(),
		// The listing came from the default namespace, but the row names prod.
		State: fakeResolver{name: "prod", namespace: "default", kind: kinds.Namespace},
		Save:  states.Save,
	}
	node, err := command.Execute(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if node.Label != "Namespace/prod" {
		t.Errorf("root = %q, want Namespace/prod", node.Label)
	}
}
