package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
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

func TestIndexFlag(t *testing.T) {
	if got := indexFlag(false); got != "--no-index" {
		t.Errorf("indexFlag(false) = %q, want --no-index", got)
	}
	if got := indexFlag(true); got != "" {
		t.Errorf("indexFlag(true) = %q, want empty", got)
	}
}

func TestScopeCaption(t *testing.T) {
	if got := scopeCaption("Namespace", "prod"); got != "Namespace · prod" {
		t.Errorf("scopeCaption = %q, want %q", got, "Namespace · prod")
	}
	if got := scopeCaption("Deployment/web", ""); got != "Deployment/web" {
		t.Errorf("scopeCaption dropped empty parts incorrectly: %q", got)
	}
}

func TestTreeRegistersHTMLFlags(t *testing.T) {
	cmd := newTreeCommand(Services{})
	for _, name := range []string{"html", "port", "no-open"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

// treeHTMLServices builds a Services that can drive newTreeCommand's RunE all
// the way through a real (fake-clientset) graph walk, rather than stopping at
// the early guards Services{} hits elsewhere in this file. Kubectl must be
// set: unlike diag, tree's sweep branch always calls
// services.Kubectl.CurrentNamespace() unconditionally, with no namespace flag
// to short-circuit it.
func treeHTMLServices(t *testing.T, namespace string, objects ...runtime.Object) Services {
	t.Helper()
	return Services{
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config:  config.Default(),
		Kubectl: &fakeKubectl{namespace: namespace},
		Kubernetes: func() (kubernetes.Interface, error) {
			return fake.NewSimpleClientset(objects...), nil
		},
	}
}

// Moving the "if !htmlOpts.Enabled { return nil }" gate above render.Tree
// would make --html silently swallow the terminal tree the command always
// printed — the same "adds, never replaces" regression
// TestDiagSweepWithHTMLStillPrintsTheTerminalTriage guards for diag. Only
// driving RunE for real pins the *order* of the two statements.
func TestTreeSweepWithHTMLStillPrintsTheTerminalTree(t *testing.T) {
	sink := captureRender(t)
	cmd := newTreeCommand(treeHTMLServices(t, "prod"))
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"html", "true"}, {"no-open", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Namespace/prod") {
		t.Errorf("terminal output = %q, want the tree to still print with --html set", sink.String())
	}
}

// Same regression, indexed branch: render.Tree must still fire before the
// html gate, even though the node is about to be re-rendered as a page.
func TestTreeIndexedWithHTMLStillPrintsTheTerminalTree(t *testing.T) {
	sink := captureRender(t)
	services := treeHTMLServices(t, "prod", &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
	})
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{{Name: "web", Kind: kinds.Deployment}}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newTreeCommand(services)
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"html", "true"}, {"no-open", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, []string{"1"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Deployment/web") {
		t.Errorf("terminal output = %q, want the tree to still print with --html set", sink.String())
	}
}

// The two tests above set --html=true, where moving the gate above the
// render call is invisible: the gate never returns early either way. This
// pins the far more common path — --html left off — where a moved gate would
// return before the render call ever runs.
func TestTreeWithoutHTMLPrintsNoServeAnnouncement(t *testing.T) {
	sink := captureRender(t)
	cmd := newTreeCommand(treeHTMLServices(t, "prod"))
	cmd.SetContext(context.Background())

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Namespace/prod") {
		t.Errorf("terminal output = %q, want the tree to print with --html left off", sink.String())
	}
	if strings.Contains(sink.String(), "serving at") {
		t.Errorf("terminal output = %q, want no serve announcement with --html left off", sink.String())
	}
}

// Indexing is on by default: a plain sweep with no flags should assign
// indexes and save them, so a later command can resolve index 1.
func TestTreeDefaultSweepIndexesAndSavesState(t *testing.T) {
	captureRender(t)
	services := treeHTMLServices(t, "prod", &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
	})
	cmd := newTreeCommand(services)
	cmd.SetContext(context.Background())

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	name, _, kind, err := services.State.Fields(1)
	if err != nil {
		t.Fatalf("Fields(1) after default sweep: %v", err)
	}
	if name != "web" || kind != kinds.Deployment {
		t.Errorf("Fields(1) = (%q, %s), want (web, Deployment)", name, kind)
	}
}

// --no-index opts out of both index assignment and the state save.
func TestTreeNoIndexSweepSkipsIndexingAndState(t *testing.T) {
	captureRender(t)
	services := treeHTMLServices(t, "prod", &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
	})
	cmd := newTreeCommand(services)
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("no-index", "true"); err != nil {
		t.Fatalf("set --no-index: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if _, _, _, err := services.State.Fields(1); !errors.Is(err, state.ErrNoState) {
		t.Errorf("Fields(1) after --no-index sweep: err = %v, want ErrNoState", err)
	}
}

// A Namespace row is the one branch most likely to get name/namespace
// backwards (see TestTreeOnNamespaceIndexGraphsThatNamespace's own comment):
// the row was listed from one namespace but names another. This drives that
// branch through --html end to end rather than just asserting on
// TreeCommand.Execute's return value.
func TestTreeIndexedOnNamespaceRowWithHTMLUsesNamespaceScope(t *testing.T) {
	sink := captureRender(t)
	services := treeHTMLServices(t, "default")
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{{Name: "prod", Kind: kinds.Namespace}}),
		Namespace: "default",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newTreeCommand(services)
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"html", "true"}, {"no-open", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, []string{"1"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	// The row was listed under "default", but it names "prod" — the tree
	// graphed and the page titled must both follow the row's own name.
	if !strings.Contains(sink.String(), "Namespace/prod") {
		t.Errorf("terminal output = %q, want Namespace/prod graphed, not the listing namespace", sink.String())
	}
}

// ExecuteAllNamespaces walks every namespace, one root per namespace, sorted
// (Namespaces itself is pinned separately in internal/graph; this is the
// integration between that ordering and the per-namespace BuildNamespace walk).
func TestTreeExecuteAllNamespacesReturnsOneRootPerNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"}},
	)
	command := TreeCommand{Builder: graph.Builder{Client: client}}
	roots, err := command.ExecuteAllNamespaces(context.Background())
	if err != nil {
		t.Fatalf("ExecuteAllNamespaces: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %d, want 2", len(roots))
	}
	// Sorted: "default" before "prod".
	if roots[0].Label != "Namespace/default" {
		t.Errorf("roots[0] = %q, want Namespace/default", roots[0].Label)
	}
	if roots[1].Label != "Namespace/prod" {
		t.Errorf("roots[1] = %q, want Namespace/prod", roots[1].Label)
	}
}

// -A never indexes, matching kx get -A and kx diag -A: names repeat across
// namespaces, so there is nothing stable to index.
func TestTreeExecuteAllNamespacesNeverIndexes(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"}},
	)
	command := TreeCommand{Builder: graph.Builder{Client: client}}
	roots, err := command.ExecuteAllNamespaces(context.Background())
	if err != nil {
		t.Fatalf("ExecuteAllNamespaces: %v", err)
	}
	if len(roots) != 1 || len(roots[0].Children) != 1 {
		t.Fatalf("roots = %+v, want exactly one namespace with one workload", roots)
	}
	if roots[0].Children[0].Index != 0 {
		t.Errorf("workload index = %d, want 0 (unindexed)", roots[0].Children[0].Index)
	}
}

func TestTreeRegistersNamespaceFlags(t *testing.T) {
	cmd := newTreeCommand(Services{})
	for _, name := range []string{"namespace", "all-namespaces"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

func TestTreeRejectsNamespaceAndAllNamespacesTogether(t *testing.T) {
	cmd := newTreeCommand(Services{})
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.Flags().Set("all-namespaces", "true"); err != nil {
		t.Fatalf("set --all-namespaces: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("-n and -A were accepted together")
	} else if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("err = %v", err)
	}
}

// -A is never indexed regardless of flags, so --no-index alongside it is a
// harmless no-op rather than a conflict — there's no more explicit --index
// flag for -A to conflict with, unlike the old opt-in flag shape.
func TestTreeAllNamespacesWithNoIndexIsAcceptedAsANoOp(t *testing.T) {
	captureRender(t)
	cmd := newTreeCommand(treeHTMLServices(t, "prod"))
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"all-namespaces", "true"}, {"no-index", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("-A and --no-index should be accepted together: %v", err)
	}
}

func TestTreeRejectsScopeFlagWithIndexArgument(t *testing.T) {
	for _, flag := range [][2]string{{"namespace", "prod"}, {"all-namespaces", "true"}} {
		cmd := newTreeCommand(Services{})
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
		if err := cmd.RunE(cmd, []string{"1"}); err == nil {
			t.Errorf("--%s was accepted alongside an index", flag[0])
		} else if !strings.Contains(err.Error(), "cannot be combined with an index") {
			t.Errorf("--%s err = %v", flag[0], err)
		}
	}
}

// Moving the "if !htmlOpts.Enabled { return nil }" gate above the -A render
// loop would make --html silently swallow the terminal trees the command
// always prints — the same "adds, never replaces" regression the other
// branches are pinned against.
func TestTreeAllNamespacesWithHTMLStillPrintsEveryTerminalTree(t *testing.T) {
	sink := captureRender(t)
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging"}},
	)
	services := Services{
		State:  &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config: config.Default(),
		Kubernetes: func() (kubernetes.Interface, error) {
			return client, nil
		},
	}
	cmd := newTreeCommand(services)
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"all-namespaces", "true"}, {"html", "true"}, {"no-open", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	for _, want := range []string{"Namespace/prod", "Namespace/staging"} {
		if !strings.Contains(sink.String(), want) {
			t.Errorf("terminal output = %q, want %q to still print with --html set", sink.String(), want)
		}
	}
}

// Every other scope-spanning listing (kx scan -A, kx diag -A, and tree's own
// -n branch) prints a caption before its output; -A was the one gap.
func TestTreeAllNamespacesPrintsAScopeBannerBeforeTheForest(t *testing.T) {
	sink := captureRender(t)
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
	)
	services := Services{
		State:  &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config: config.Default(),
		Kubernetes: func() (kubernetes.Interface, error) {
			return client, nil
		},
	}
	cmd := newTreeCommand(services)
	cmd.SetContext(stoppedContext())
	if err := cmd.Flags().Set("all-namespaces", "true"); err != nil {
		t.Fatalf("set --all-namespaces: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	banner := strings.Index(sink.String(), "Namespace · all namespaces")
	tree := strings.Index(sink.String(), "Namespace/prod")
	if banner < 0 {
		t.Fatalf("terminal output = %q, want a scope banner", sink.String())
	}
	if tree >= 0 && banner > tree {
		t.Errorf("banner printed after the tree it scopes:\n%s", sink.String())
	}
}
