package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

const watchPodsHeader = "EVENT      NAME             READY   STATUS    RESTARTS   AGE"
const watchPodsAdded = "ADDED      nginx-abc-xyz    1/1     Running   0          5d"
const watchPodsDeleted = "DELETED    nginx-abc-xyz    1/1     Running   0          5d"

// `kx get pods --watch` used to hang forever: Run() buffers all of kubectl's
// stdout and only returns once the process exits, but `kubectl get --watch`
// never exits on its own. It now streams a live-redrawing table instead,
// tracking ADDED/MODIFIED/DELETED via kubectl's --output-watch-events flag,
// rather than the raw passthrough an earlier fix used.
func TestGetWatchDefaultTableUsesLiveRedraw(t *testing.T) {
	for _, flag := range []string{"--watch", "-w"} {
		t.Run(flag, func(t *testing.T) {
			kube := &fakeKubectl{
				watchLines: []string{watchPodsHeader, watchPodsAdded},
			}
			services := switchServices(t, kube)

			var out bytes.Buffer
			render.SetOutput(&out, &out, "github-dark")

			if err := runGet(services, "pods", []string{flag}, getOptions{}); err != nil {
				t.Fatalf("runGet: %v", err)
			}

			if len(kube.args) != 0 {
				t.Errorf("Run was called with %v; watch must not go through the buffered path", kube.args)
			}
			want := []string{"get", "pods", flag, "--output-watch-events"}
			if joinArgs(kube.watchArgs) != joinArgs(want) {
				t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
			}
			// RedrawTable is a no-op off-terminal (internal/render/redraw_test.go
			// covers its actual drawing, and TestWatchRows* in watch_test.go covers
			// the row tracking), so a bytes.Buffer-backed test never sees the row
			// itself — only the note printed unconditionally before the loop starts.
			if !strings.Contains(out.String(), "can't be indexed") {
				t.Errorf("output = %q, want a note that watch listings aren't indexed", out.String())
			}

			if _, err := services.State.LoadHistory(); !errors.Is(err, state.ErrNoState) {
				t.Errorf("LoadHistory error = %v, want ErrNoState — a watch listing must not be saved", err)
			}
		})
	}
}

func TestGetWatchDoesNotDuplicateOutputWatchEvents(t *testing.T) {
	kube := &fakeKubectl{watchLines: []string{watchPodsHeader}}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"--watch", "--output-watch-events"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	count := strings.Count(joinArgs(kube.watchArgs), "--output-watch-events")
	if count != 1 {
		t.Errorf("--output-watch-events appears %d times in %v, want 1", count, kube.watchArgs)
	}
}

func TestGetWatchRemovesDeletedRows(t *testing.T) {
	kube := &fakeKubectl{
		watchLines: []string{watchPodsHeader, watchPodsAdded, watchPodsDeleted},
	}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	// Off-terminal, RedrawTable is a no-op, so this only proves runWatch
	// doesn't error walking an ADDED-then-DELETED sequence down to zero rows.
	// TestWatchRowsDeletedRemoves is the behavioral proof for the removal.
}

// -o json is non-tabular: it keeps the raw-streaming passthrough, since
// re-theming JSON doesn't make sense.
func TestGetWatchNonTabularOutputKeepsPassthrough(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch", "-o", "json"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if len(kube.interactive) != 1 {
		t.Fatalf("RunInteractive called %d times, want 1", len(kube.interactive))
	}
	want := []string{"get", "pods", "--watch", "-o", "json"}
	if joinArgs(kube.interactive[0]) != joinArgs(want) {
		t.Errorf("interactive args = %v, want %v", kube.interactive[0], want)
	}
	if !strings.Contains(out.String(), "can't be indexed") {
		t.Errorf("output = %q, want a note that watch listings aren't indexed", out.String())
	}
}

// -A also uses the live table: watchRows keys rows by NAMESPACE/NAME in that
// case (TestWatchRowsKeysByNamespaceAndNameForAllNamespaces is the
// collision-safety proof), so it isn't unindexed-and-passthrough-only.
func TestGetWatchAllNamespacesUsesLiveRedraw(t *testing.T) {
	watchAllNamespacesHeader := "EVENT      NAMESPACE   NAME             READY   STATUS    RESTARTS   AGE"
	watchAllNamespacesAdded := "ADDED      prod        nginx-abc-xyz    1/1     Running   0          5d"
	kube := &fakeKubectl{
		watchLines: []string{watchAllNamespacesHeader, watchAllNamespacesAdded},
	}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch", "-A"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "--watch", "-A", "--output-watch-events"}
	if joinArgs(kube.watchArgs) != joinArgs(want) {
		t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
	}
	// The caption itself only renders through RedrawTable, which is a no-op
	// off-terminal — TestWatchNamespaceAllNamespaces in watch_test.go is the
	// direct proof that -A resolves to "all namespaces".
}

// A range argument resolves to every name it spans, the same as typing out
// the equivalent literal indexes, so `kx get pods 1..2` relists both pods.
func TestGetIndexRangeRelist(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod"}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	if err := runGet(services, "pods", []string{"1..2"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "nginx-abc-xyz", "redis-def-uvw", "-n", "prod"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// A kubectl flag value can legitimately contain ".." — JSONPath's recursive
// descent, e.g. -o jsonpath={..metadata.name} — and must reach kubectl
// untouched rather than being mistaken for a range token. Range/int
// recognition is restricted to the leading run specifically so a value like
// this, which never leads, is never inspected for it.
func TestGetPassesThroughDoubleDotFlagValues(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-o", "jsonpath={..metadata.name}"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "-o", "jsonpath={..metadata.name}"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// Indexes only lead; one after a kubectl flag is no longer special-cased and
// reaches kubectl as a literal positional the way any other non-index token
// does — the flip side of TestGetPassesThroughDoubleDotFlagValues, and the
// only way to stop scanning every argument for something index-shaped
// without also mistaking a flag's own value for one.
func TestGetIndexAfterFlagIsNotResolved(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-n", "prod", "3"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "-n", "prod", "3"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// An indexed argument still resolves to a name before the watch flag routes
// to the live table, so `kx get pods 1 --watch` watches the right pod.
func TestGetWatchResolvesIndexFirst(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod", watchLines: []string{watchPodsHeader}}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	if err := runGet(services, "pods", []string{"1", "--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "nginx-abc-xyz", "--watch", "-n", "prod", "--output-watch-events"}
	if joinArgs(kube.watchArgs) != joinArgs(want) {
		t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
	}
}

// Indexes from an -A listing can resolve into different namespaces, and kubectl
// cannot fetch named resources across namespaces in one call. The relist issues
// one call per namespace and stitches the results, so `kx get pods 1 2` means
// the same thing on an -A listing as on any other.
func TestGetRelistAcrossNamespacesCallsKubectlPerNamespace(t *testing.T) {
	kube := &fakeKubectl{
		outputs: []string{
			// The -A listing being indexed.
			"NAMESPACE   NAME       READY   STATUS    RESTARTS   AGE\n" +
				"default     api-7d8f   1/1     Running   0          5d\n" +
				"staging     api-7d8f   1/1     Running   0          3d",
			// One reply per namespace, each without a NAMESPACE column,
			// which is what kubectl returns for a namespaced query.
			"NAME       READY   STATUS    RESTARTS   AGE\n" +
				"api-7d8f   1/1     Running   0          5d",
			"NAME       READY   STATUS    RESTARTS   AGE\n" +
				"api-7d8f   1/1     Running   0          3d",
		},
	}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	if err := runGet(services, "pods", []string{"1", "2"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	relists := kube.calls[1:]
	if len(relists) != 2 {
		t.Fatalf("made %d relist calls, want one per namespace: %v", len(relists), relists)
	}
	if got := joinArgs(relists[0]); got != "get pods api-7d8f -n default" {
		t.Errorf("first call = %q, want it scoped to default", got)
	}
	if got := joinArgs(relists[1]); got != "get pods api-7d8f -n staging" {
		t.Errorf("second call = %q, want it scoped to staging", got)
	}
}

// The stitched table has to keep saying which namespace each row came from, or
// the relisted indexes resolve no better than the ones they replaced.
func TestGetRelistAcrossNamespacesKeepsTheNamespaceColumn(t *testing.T) {
	kube := &fakeKubectl{
		outputs: []string{
			"NAMESPACE   NAME       READY   STATUS    RESTARTS   AGE\n" +
				"default     api-7d8f   1/1     Running   0          5d\n" +
				"staging     api-7d8f   1/1     Running   0          3d",
			"NAME       READY   STATUS    RESTARTS   AGE\n" +
				"api-7d8f   1/1     Running   0          5d",
			"NAME       READY   STATUS    RESTARTS   AGE\n" +
				"api-7d8f   1/1     Running   0          3d",
		},
	}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	if err := runGet(services, "pods", []string{"1", "2"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	current, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries := current.Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("relist saved %d resources, want 2: %+v", len(entries), entries)
	}
	if entries[0].Namespace != "default" || entries[1].Namespace != "staging" {
		t.Errorf("namespaces = %q, %q; want default, staging",
			entries[0].Namespace, entries[1].Namespace)
	}
}

// A relist whose indexes all sit in one namespace keeps the single call and the
// plain table it has always produced — no NAMESPACE column appears just because
// the machinery for one exists.
func TestGetRelistWithinOneNamespaceStaysASingleCall(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod"}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	before := len(kube.calls)
	if err := runGet(services, "pods", []string{"1", "2"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	if got := len(kube.calls) - before; got != 1 {
		t.Errorf("made %d calls for a single-namespace relist, want 1", got)
	}
	want := "get pods nginx-abc-xyz redis-def-uvw -n prod"
	if got := joinArgs(kube.calls[len(kube.calls)-1]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	// The call count and args alone cannot tell the two paths apart — with one
	// group the stitching path issues the same single call. What separates them
	// is the shape of what it saves: the stitched table carries a NAMESPACE
	// column and per-resource namespaces, which a single-namespace listing must
	// not grow.
	current, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.Namespace != "prod" {
		t.Errorf("entry namespace = %q, want prod", current.Namespace)
	}
	for _, entry := range current.Resources.Entries() {
		if entry.Namespace != "" {
			t.Errorf("resource %q gained namespace %q from the stitching path",
				entry.Name, entry.Namespace)
		}
	}
}

// kubectl watches one named resource at a time. Forwarding a multi-index watch
// produced an answer about the wrong thing: names were scoped to the first
// group's namespace, so a selection spanning namespaces came back as
// "pods ... not found" — a resource that is gone, rather than a request kubectl
// will not serve.
func TestGetWatchRefusesSeveralIndexes(t *testing.T) {
	kube := &fakeKubectl{}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		AllNamespaces: true,
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "web", Kind: kinds.Pod, Namespace: "prod"},
			{Name: "api", Kind: kinds.Pod, Namespace: "staging"},
		}),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	quietRender(t)

	err := runGet(services, "pods", []string{"1", "2", "--watch"}, getOptions{})
	if err == nil {
		t.Fatal("multi-index watch was accepted; want it refused")
	}
	if !strings.Contains(err.Error(), "--watch takes a single resource") {
		t.Errorf("error = %q, want it to name the single-resource rule", err)
	}
	if len(kube.watchArgs) != 0 {
		t.Errorf("opened a watch anyway: %v", kube.watchArgs)
	}
}

// One index still watches — the guard is about the number of resources, not
// about watching being unavailable to indexed resources.
func TestGetWatchStillAcceptsOneIndex(t *testing.T) {
	kube := &fakeKubectl{watchLines: []string{watchPodsHeader, watchPodsAdded}}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Namespace: "prod",
		Resources: state.NewResources([]string{"nginx-abc-xyz"}, kinds.Pod),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	quietRender(t)

	if err := runGet(services, "pods", []string{"1", "--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if len(kube.watchArgs) == 0 {
		t.Error("no watch was opened for a single index")
	}
}

// A namespace-scope flag on a cluster-scoped kind is a contradiction, and kx
// refuses it the way it already refuses one beside an index.
//
// It used to be worse than meaningless. kubectl ignores -A for a cluster-scoped
// kind and returns a table with no NAMESPACE column, which is exactly the shape
// Execute treats as unplaceable — so `kx get nodes -A` printed raw, unnumbered
// kubectl output and saved nothing, silently leaving whatever listing was in
// state resolving the indexes underneath it. `kx get pods` then `kx get nodes
// -A` then `kx describe 1` described a pod, under a table of nodes.
func TestGetClusterScopedRefusesScopeFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-A"}, {"--all-namespaces"}, {"-n", "prod"}, {"--namespace", "prod"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			quietRender(t)
			kube := &fakeKubectl{output: clusterScopedNodesTable}
			services := switchServices(t, kube)

			err := runGet(services, "nodes", args, getOptions{})
			if err == nil {
				t.Fatal("runGet accepted a namespace flag on a cluster-scoped kind")
			}
			if !strings.Contains(err.Error(), args[0]) {
				t.Errorf("error = %q, want it to name %q", err, args[0])
			}
			if !strings.Contains(err.Error(), "Node") {
				t.Errorf("error = %q, want it to name the kind it refused for", err)
			}
			if len(kube.calls) != 0 {
				t.Errorf("reached kubectl with %v; the flag is refused before the cluster is read", kube.calls)
			}
		})
	}
}

// The refusal must not cost the listing that made kx worth using: a
// cluster-scoped kind with no scope flag still lists, numbers and saves.
func TestGetClusterScopedWithoutScopeFlagsStillIndexes(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{output: clusterScopedNodesTable}
	services := switchServices(t, kube)

	if err := runGet(services, "nodes", nil, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	current, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries := current.Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("saved %d resources, want 2: %+v", len(entries), entries)
	}
	if entries[0].Name != "node-a" || entries[0].Kind != kinds.Node {
		t.Errorf("first entry = %+v, want node-a/Node", entries[0])
	}
}

// Namespaced kinds are untouched — -A is the flag half of kx's index model and
// a guard that caught it would break every spanning listing.
func TestGetNamespacedStillAcceptsScopeFlags(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{
		output: "NAMESPACE   NAME       READY   STATUS    RESTARTS   AGE\n" +
			"default     api-7d8f   1/1     Running   0          5d",
	}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if len(kube.calls) != 1 {
		t.Fatalf("made %d kubectl calls, want 1", len(kube.calls))
	}
}

// A kind kx cannot place — a CRD with no discovery cache — keeps today's
// behaviour rather than being refused on a guess. Refusing on "not known to be
// namespaced" would reject -A for every custom resource on a machine whose
// discovery cache has not been populated.
func TestGetUnknownKindStillAcceptsScopeFlags(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{
		output: "NAMESPACE   NAME   AGE\ndefault     gw-a   1d",
	}
	services := switchServices(t, kube)

	err := runGet(services, "gateways.gateway.networking.k8s.io", []string{"-A"}, getOptions{})
	if err != nil {
		t.Fatalf("runGet refused an unplaceable kind: %v", err)
	}
}

// Resolving indexes from a cluster-scoped listing must not scope the relist to
// a namespace, or the guard would fire on kx's own argv. `kx get nodes 1` is a
// relist, not a scope request.
//
// Asserted on the argv rather than only on the absence of an error: the guard
// reads the same slice the relist appends to, so "no -n was added" and "the
// relist was not refused" are the same fact, and the argv is the half that
// still fails if either the backfill or the append condition regresses.
func TestGetClusterScopedRelistByIndexIsNotScopedToANamespace(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{output: clusterScopedNodesTable}
	services := switchServices(t, kube)

	if err := runGet(services, "nodes", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	if err := runGet(services, "nodes", []string{"1"}, getOptions{}); err != nil {
		t.Fatalf("relist by index was refused: %v", err)
	}

	if len(kube.calls) != 2 {
		t.Fatalf("made %d kubectl calls, want 2 (listing then relist): %v", len(kube.calls), kube.calls)
	}
	relist := joinArgs(kube.calls[1])
	if !strings.Contains(relist, "node-a") {
		t.Fatalf("relist = %q, want it to name the resolved node", relist)
	}
	if strings.Contains(relist, "-n ") {
		t.Errorf("relist = %q, want no namespace flag — a Node is not in one", relist)
	}
}

const clusterScopedNodesTable = "NAME      STATUS   ROLES           AGE   VERSION\n" +
	"node-a    Ready    control-plane   1d    v1.34.3\n" +
	"node-b    Ready    <none>          1d    v1.34.3"
