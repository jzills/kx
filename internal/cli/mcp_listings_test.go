package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// These pin kx mcp --write-listings: each listing tool saves exactly the entry
// the CLI command it mirrors would, tagged as the agent's, and every index it
// returns is that row's position in the saved entry.

// writingDeps is mcpTestDeps with --write-listings on.
func writingDeps(t *testing.T, kube *recordingKubectl) mcpDeps {
	t.Helper()
	deps := mcpTestDeps(t, kube)
	deps.WriteListings = true
	return deps
}

// cliState is a second state service, on its own file, for driving the CLI
// command a tool mirrors — so a test compares the tool's entry with the one
// the CLI actually writes rather than with a hand-built expectation of it.
func cliState(t *testing.T) *state.Service {
	t.Helper()
	return &state.Service{
		MaxHistory: 10, Path: filepath.Join(t.TempDir(), "cli.json"),
		Context: func() string { return "test" },
	}
}

// onlyTaggedEntry asserts the history holds exactly one entry, tagged as an
// agent's and stamped with the live context, and returns it.
func onlyTaggedEntry(t *testing.T, service *state.Service) state.State {
	t.Helper()
	history, err := service.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history.States) != 1 {
		t.Fatalf("%d entries saved, want exactly 1", len(history.States))
	}
	entry := history.States[0]
	if entry.Source != state.SourceMCP {
		t.Errorf("source = %q, want %q", entry.Source, state.SourceMCP)
	}
	if entry.Context != "test" {
		t.Errorf("context = %q, want the live context test", entry.Context)
	}
	return entry
}

// assertSavedLikeTheCLI compares the tool's entry with the CLI's, ignoring
// only the tag, which the CLI never sets.
func assertSavedLikeTheCLI(t *testing.T, got state.State, cli *state.Service) {
	t.Helper()
	want, err := cli.Load()
	if err != nil {
		t.Fatalf("CLI Load: %v", err)
	}
	got.Source = ""
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("saved entry differs from the CLI's:\ngot  %s\nwant %s", gotJSON, wantJSON)
	}
}

// assertResolves asserts index n resolves, through the state service the
// CLI's commands use, to the resource the tool showed under it.
func assertResolves(t *testing.T, service *state.Service, n int, kind kinds.Kind, name, namespace string) {
	t.Helper()
	gotName, gotNamespace, gotKind, err := service.Fields(n)
	if err != nil {
		t.Errorf("Fields(%d): %v", n, err)
		return
	}
	if gotName != name || gotKind != kind || (namespace != "" && gotNamespace != namespace) {
		t.Errorf("index %d resolves to %s/%s in %q, the tool showed %s/%s in %q",
			n, gotKind, gotName, gotNamespace, kind, name, namespace)
	}
}

type indexedListOutput struct {
	Resources []struct {
		Index     int    `json:"index"`
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"resources"`
}

func TestWriteListingsListResourcesSavesLikeKxGet(t *testing.T) {
	allOutput := "NAMESPACE   NAME   READY   STATUS    RESTARTS   AGE\n" +
		"a           web    1/1     Running   0          1d\n" +
		"b           web    1/1     Running   0          1d\n"
	nodes := "NAME     STATUS   ROLES    AGE   VERSION\n" +
		"node-a   Ready    <none>   1d    v1.30\n" +
		"node-b   Ready    <none>   1d    v1.30\n"
	for _, tc := range []struct {
		name   string
		output string
		input  map[string]any
		// cliArgs is the `kx get` invocation that lists the same thing.
		resource string
		cliArgs  []string
	}{
		{"namespace", podsOutput, map[string]any{"kind": "pods"}, "pods", []string{"-n", "prod"}},
		{"all namespaces", allOutput, map[string]any{"kind": "pods", "allNamespaces": true}, "pods", []string{"-A"}},
		{"cluster-scoped", nodes, map[string]any{"kind": "nodes"}, "nodes", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := writingDeps(t, &recordingKubectl{output: tc.output, namespace: "prod"})
			var out indexedListOutput
			decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources", tc.input), &out)

			entry := onlyTaggedEntry(t, deps.State)
			cli := cliState(t)
			if _, _, err := (GetCommand{
				Kubectl: &recordingKubectl{output: tc.output, namespace: "prod"}, State: cli, Index: index.Service{},
			}).Execute(tc.resource, "", tc.cliArgs); err != nil {
				t.Fatal(err)
			}
			assertSavedLikeTheCLI(t, entry, cli)

			if len(out.Resources) != 2 {
				t.Fatalf("resources = %+v, want 2", out.Resources)
			}
			for i, row := range out.Resources {
				if row.Index != i+1 {
					t.Errorf("row %d carries index %d", i, row.Index)
				}
				assertResolves(t, deps.State, row.Index, kinds.Kind(row.Kind), row.Name, row.Namespace)
			}
		})
	}
}

// A limit cuts what is returned, not what is saved: the indexes shown are
// the first rows of the whole listing, and the rest still resolve.
func TestWriteListingsListResourcesLimitKeepsTheWholeListing(t *testing.T) {
	var table strings.Builder
	table.WriteString("NAME   AGE\n")
	for i := range 5 {
		fmt.Fprintf(&table, "cm-%d   1d\n", i)
	}
	deps := writingDeps(t, &recordingKubectl{output: table.String()})
	var out indexedListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "cm", "limit": 2}), &out)
	if len(out.Resources) != 2 || out.Resources[1].Index != 2 {
		t.Fatalf("resources = %+v, want cm-0 and cm-1 at 1 and 2", out.Resources)
	}
	if entry := onlyTaggedEntry(t, deps.State); entry.Resources.Len() != 5 {
		t.Errorf("saved %d resources, want all 5", entry.Resources.Len())
	}
	assertResolves(t, deps.State, 5, kinds.ConfigMap, "cm-4", "")
}

// `kx get ns` pushes onto the stack and refreshes the namespace slot, which is
// what `kx ns <n>` reads. The agent's listing does exactly the same, and the
// slot's copy carries the tag too, because it is the same entry.
func TestWriteListingsNamespacesFillTheSlotLikeKxGetNs(t *testing.T) {
	namespaces := "NAME      STATUS   AGE\n" +
		"default   Active   1d\n" +
		"prod      Active   1d\n"
	deps := writingDeps(t, &recordingKubectl{output: namespaces})
	var out indexedListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "namespaces"}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	cli := cliState(t)
	if _, _, err := (GetCommand{
		Kubectl: &recordingKubectl{output: namespaces}, State: cli, Index: index.Service{},
	}).Execute("namespaces", "", nil); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)

	history, err := deps.State.LoadHistory()
	if err != nil {
		t.Fatal(err)
	}
	slot, ok := history.Named[kinds.Namespace]
	if !ok {
		t.Fatal("no namespace slot saved; kx get ns fills it")
	}
	if slot.Source != state.SourceMCP {
		t.Errorf("slot source = %q, want %q", slot.Source, state.SourceMCP)
	}
	cliHistory, err := cli.LoadHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(cliHistory.Named) != len(history.Named) {
		t.Errorf("slots = %v, the CLI's = %v", history.Named, cliHistory.Named)
	}
	name, _, err := deps.State.FieldsNamed(out.Resources[1].Index, kinds.Namespace)
	if err != nil || name != "prod" {
		t.Errorf("kx ns %d = %q, %v; want prod", out.Resources[1].Index, name, err)
	}
}

// Review Focus 2: the agent re-lists what the user's cursor is on. The
// cursor-only dedupe replaces the user's entry, as a second `kx get` would,
// and the replacement says it is the agent's — so the confirm prompt does.
func TestWriteListingsSameQueryReplacesTheCursorEntryAndTagsIt(t *testing.T) {
	deps := writingDeps(t, &recordingKubectl{output: podsOutput, namespace: "prod"})
	if _, _, err := (GetCommand{
		Kubectl: &recordingKubectl{output: podsOutput, namespace: "prod"}, State: deps.State, Index: index.Service{},
	}).Execute("pods", "", []string{"-n", "prod"}); err != nil {
		t.Fatal(err)
	}
	if entry, err := deps.State.Load(); err != nil || entry.Source != "" {
		t.Fatalf("the user's own listing reads as source %q, %v", entry.Source, err)
	}

	callTool(t, connectMCP(t, deps), "list_resources", map[string]any{"kind": "pods"})
	onlyTaggedEntry(t, deps.State)
}

// Review Focus 3, and the sweep's shape: every swept resource is saved in
// severity order, as kx diag saves it, so an unhealthy-only answer shows the
// unhealthy rows' own positions — and the healthy row, left out of it, still
// resolves at the number a full sweep shows it under.
func TestWriteListingsDiagnoseSweepIndexesResolveWithHealthyRowsLeftOut(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		healthyDeployment("web", "prod"), brokenDeployment("api", "prod"), brokenDeployment("db", "prod"))
	deps.WriteListings = true
	session := connectMCP(t, deps)

	var out diagnoseResult
	decodeStructured(t, callTool(t, session, "diagnose", map[string]any{}), &out)
	if len(out.Diagnosis.Resources) != 2 {
		t.Fatalf("resources = %+v, want the two unhealthy ones", out.Diagnosis.Resources)
	}
	for i, resource := range out.Diagnosis.Resources {
		if resource.Index != i+1 {
			t.Errorf("%s carries index %d, want %d — unhealthy rows sort first", resource.Name, resource.Index, i+1)
		}
		if resource.Name == "web" {
			t.Errorf("the healthy web was returned without full")
		}
		assertResolves(t, deps.State, resource.Index, kinds.Kind(resource.Kind), resource.Name, "prod")
	}
	entry := onlyTaggedEntry(t, deps.State)
	if entry.Resources.Len() != 3 {
		t.Errorf("saved %d resources, want all 3 swept — an unhealthy-only entry shifts the numbers", entry.Resources.Len())
	}

	var full diagnoseResult
	decodeStructured(t, callTool(t, session, "diagnose", map[string]any{"full": true}), &full)
	if len(full.Diagnosis.Resources) != 3 || full.Diagnosis.Resources[2].Name != "web" ||
		full.Diagnosis.Resources[2].Index != 3 {
		t.Fatalf("full sweep = %+v, want the healthy web last at 3", full.Diagnosis.Resources)
	}
	assertResolves(t, deps.State, 3, kinds.Deployment, "web", "prod")

	// The same sweep again is the same view, so it replaced rather than pushed.
	entry = onlyTaggedEntry(t, deps.State)
	client, err := deps.Kubernetes()
	if err != nil {
		t.Fatal(err)
	}
	cli := cliState(t)
	if _, err := (TriageCommand{Diagnostics: diagnostics.New(client), Save: cli.Save}).
		Execute(context.Background(), "prod", false, false); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
}

// The diagnose sweep across every namespace saves like kx diag -A: no entry
// namespace, each resource carrying its own.
func TestWriteListingsDiagnoseSweepAcrossNamespacesSavesLikeKxDiagA(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		brokenDeployment("api", "a"), healthyDeployment("web", "b"))
	deps.WriteListings = true
	var out diagnoseResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "diagnose",
		map[string]any{"allNamespaces": true, "full": true}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	client, err := deps.Kubernetes()
	if err != nil {
		t.Fatal(err)
	}
	cli := cliState(t)
	if _, err := (TriageCommand{Diagnostics: diagnostics.New(client), Save: cli.Save}).
		Execute(context.Background(), "", true, true); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
	for _, resource := range out.Diagnosis.Resources {
		assertResolves(t, deps.State, resource.Index, kinds.Kind(resource.Kind), resource.Name, "")
	}
}

// collectTreeIndexes walks roots and returns every indexed node, asserting
// that each resource node carries one — containers go without, as does a
// namespace walk's Namespace root, which kx tree never numbers either.
func collectTreeIndexes(t *testing.T, roots []jsonTreeNode) []jsonTreeNode {
	t.Helper()
	var indexed []jsonTreeNode
	var walk func(node jsonTreeNode, root bool)
	walk = func(node jsonTreeNode, root bool) {
		switch {
		case node.Kind == "" && node.Index != 0:
			t.Errorf("container %s carries index %d", node.Name, node.Index)
		case root && node.Kind == string(kinds.Namespace) && node.Index == 0:
		case node.Kind != "" && node.Index == 0:
			t.Errorf("%s/%s carries no index", node.Kind, node.Name)
		case node.Index != 0:
			indexed = append(indexed, node)
		}
		for _, child := range node.Children {
			walk(child, false)
		}
	}
	for _, root := range roots {
		walk(root, true)
	}
	return indexed
}

func TestWriteListingsTreeSavesLikeKxTree(t *testing.T) {
	deps := mcpTreeDeps(t)
	deps.WriteListings = true
	var out treeResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "tree", map[string]any{}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	client, err := deps.Kubernetes()
	if err != nil {
		t.Fatal(err)
	}
	cli := cliState(t)
	if _, err := (TreeCommand{Builder: graph.Builder{Client: client}, Save: cli.Save}).
		ExecuteNamespace(context.Background(), "prod", true); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)

	indexed := collectTreeIndexes(t, out.Tree.Roots)
	if len(indexed) != entry.Resources.Len() || len(indexed) == 0 {
		t.Errorf("%d nodes indexed, %d saved", len(indexed), entry.Resources.Len())
	}
	for _, node := range indexed {
		assertResolves(t, deps.State, node.Index, kinds.Kind(node.Kind), node.Name, "")
	}
}

func TestWriteListingsTreeOfATargetSavesLikeKxTreeN(t *testing.T) {
	deps := mcpTreeDeps(t)
	deps.WriteListings = true
	var out treeResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "tree", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"},
	}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	client, err := deps.Kubernetes()
	if err != nil {
		t.Fatal(err)
	}
	cli := cliState(t)
	if _, err := (TreeCommand{Builder: graph.Builder{Client: client}, Save: cli.Save}).
		ExecuteResource(context.Background(), kinds.Deployment, "web", "prod", true); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
	for _, node := range collectTreeIndexes(t, out.Tree.Roots) {
		assertResolves(t, deps.State, node.Index, kinds.Kind(node.Kind), node.Name, "prod")
	}
}

// The CLI saves an -A forest after the walk, with no entry namespace and
// AllNamespaces set; the tool must too.
func TestWriteListingsTreeAcrossNamespacesSavesLikeKxTreeA(t *testing.T) {
	deps := mcpTreeDeps(t)
	deps.WriteListings = true
	var out treeResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "tree", map[string]any{"allNamespaces": true}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	if !entry.AllNamespaces || entry.Namespace != "" {
		t.Errorf("entry scope = %q, allNamespaces %v; want every namespace", entry.Namespace, entry.AllNamespaces)
	}
	client, err := deps.Kubernetes()
	if err != nil {
		t.Fatal(err)
	}
	cli := cliState(t)
	command := TreeCommand{Builder: graph.Builder{Client: client}, Save: cli.Save}
	_, resources, err := command.ExecuteAllNamespaces(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.save(resources, "", true, true); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
	indexed := collectTreeIndexes(t, out.Tree.Roots)
	if len(indexed) == 0 {
		t.Fatal("no node carries an index")
	}
	for _, node := range indexed {
		assertResolves(t, deps.State, node.Index, kinds.Kind(node.Kind), node.Name, "prod")
	}
}

// Review Focus 4: a limit prunes what is shown, never what is saved. The
// numbers kept are the saved positions, and a pod the cut left out still
// resolves at its own.
func TestWriteListingsTreePrunedByLimitKeepsSavedPositions(t *testing.T) {
	deps := wideTreeDeps(t)
	deps.WriteListings = true
	target := map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"}
	var cut boundedTreeResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "tree",
		map[string]any{"target": target, "limit": 5}), &cut)
	if cut.Truncated == 0 {
		t.Fatal("the limit cut nothing; the test needs a pruned tree")
	}

	shown := collectTreeIndexes(t, cut.Tree.Roots)
	if len(shown) != 5 {
		t.Fatalf("%d indexed nodes shown, want 5", len(shown))
	}
	for _, node := range shown {
		assertResolves(t, deps.State, node.Index, kinds.Kind(node.Kind), node.Name, "prod")
	}
	// web, web-abc, then six pods — containers take no index.
	entry := onlyTaggedEntry(t, deps.State)
	if entry.Resources.Len() != 8 {
		t.Fatalf("saved %d resources, want all 8 the walk indexed", entry.Resources.Len())
	}
	assertResolves(t, deps.State, 8, kinds.Pod, "web-abc-5", "prod")
}

type indexedTopOutput struct {
	Top struct {
		Rows []struct {
			Index     int    `json:"index"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"rows"`
	} `json:"top"`
}

func TestWriteListingsTopSavesLikeKxTop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outputs []string
		input   map[string]any
		kind    kinds.Kind
		run     func(TopCommand) error
	}{
		{"pods", []string{topPodsFixture, podsJSON}, map[string]any{}, kinds.Pod, func(c TopCommand) error {
			_, _, err := c.Execute("", []string{"-n", "prod"}, false)
			return err
		}},
		{"nodes", []string{nodesOutput}, map[string]any{"nodes": true}, kinds.Node, func(c TopCommand) error {
			_, _, err := c.ExecuteNodes("", nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := writingDeps(t, &recordingKubectl{outputs: append([]string{}, tc.outputs...), namespace: "prod"})
			var out indexedTopOutput
			decodeStructured(t, callTool(t, connectMCP(t, deps), "top", tc.input), &out)

			entry := onlyTaggedEntry(t, deps.State)
			if entry.Query == nil || entry.Query.Command != "top" {
				t.Errorf("query = %+v, want kx top's", entry.Query)
			}
			cli := cliState(t)
			if err := tc.run(TopCommand{
				Kubectl: &recordingKubectl{outputs: append([]string{}, tc.outputs...), namespace: "prod"},
				State:   cli, Index: index.Service{},
			}); err != nil {
				t.Fatal(err)
			}
			assertSavedLikeTheCLI(t, entry, cli)

			if len(out.Top.Rows) != 2 {
				t.Fatalf("rows = %+v, want 2", out.Top.Rows)
			}
			for _, row := range out.Top.Rows {
				if row.Index == 0 {
					t.Errorf("row %s carries no index", row.Name)
				}
				assertResolves(t, deps.State, row.Index, tc.kind, row.Name, row.Namespace)
			}
		})
	}
}

// The whole bridge, end to end: the agent lists, the user spends the agent's
// number in kx, and the destructive confirm says whose listing it came from.
func TestWriteListingsRoundTripsToTheCLIsConfirm(t *testing.T) {
	output := "NAME    READY   STATUS    RESTARTS   AGE\n" +
		"alpha   1/1     Running   0          1d\n" +
		"beta    1/1     Running   0          1d\n" +
		"gamma   1/1     Running   0          1d\n"
	deps := writingDeps(t, &recordingKubectl{output: output, namespace: "prod"})
	var out indexedListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources", map[string]any{"kind": "pods"}), &out)
	if len(out.Resources) != 3 || out.Resources[2].Index != 3 || out.Resources[2].Name != "gamma" {
		t.Fatalf("resources = %+v, want gamma at 3", out.Resources)
	}

	name, namespace, kind, err := deps.State.Fields(3)
	if err != nil || name != "gamma" || namespace != "prod" || kind != kinds.Pod {
		t.Fatalf("kx 3 = %s/%s in %s, %v; want the agent's row 3, Pod/gamma in prod", kind, name, namespace, err)
	}

	var prompted string
	if _, err := (DeleteCommand{
		Kubectl: &recordingKubectl{},
		State:   deps.State,
		Confirm: func(message string) error { prompted = message; return nil },
		Status:  noStatus,
	}).Execute(state.Ref{Index: 3}, false, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "Delete Pod/gamma in prod — from a kx mcp listing?"; prompted != want {
		t.Errorf("prompt = %q, want %q", prompted, want)
	}
}

// The flag-on guard: every tool and argument shape the read-only guard
// drives, with --write-listings on. Exactly the listing tools save — each a
// tagged entry stamped with the live context — and nothing else touches the
// history: not a single-target diagnose, events, logs, get_yaml or scan, and
// mark writes only its mark.
func TestWriteListingsSavesOnlyTheListingTools(t *testing.T) {
	deps, _, _, _, calls := readOnlyFixture(t)
	deps.WriteListings = true
	seedUserListing(t, deps)
	session := connectMCP(t, deps)

	for i, call := range calls {
		// A fresh listing of the user's own before every call, so the cursor
		// is untagged going in: a listing tool that saved leaves a tagged
		// entry there even when its save replaced an identical one.
		if err := deps.State.Save(state.State{
			Resources: state.NewResources([]string{fmt.Sprintf("user-%d", i)}, kinds.ConfigMap), Namespace: "prod",
		}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(deps.State.Path)
		if err != nil {
			t.Fatal(err)
		}
		beforeHistory, err := deps.State.LoadHistory()
		if err != nil {
			t.Fatal(err)
		}
		if result := callTool(t, session, call.tool, call.args); result.IsError {
			t.Fatalf("%s %v failed: %s", call.tool, call.args, toolText(result))
		}
		after, err := os.ReadFile(deps.State.Path)
		if err != nil {
			t.Fatal(err)
		}
		afterHistory, err := deps.State.LoadHistory()
		if err != nil {
			t.Fatal(err)
		}
		cursor := afterHistory.States[afterHistory.Cursor]

		switch {
		case call.lists:
			if cursor.Source != state.SourceMCP || cursor.Context != "test" {
				t.Errorf("%s %v: cursor entry source %q context %q, want a save tagged mcp in context test",
					call.tool, call.args, cursor.Source, cursor.Context)
			}
		case call.tool == "mark":
			if !reflect.DeepEqual(afterHistory.States, beforeHistory.States) ||
				afterHistory.Cursor != beforeHistory.Cursor ||
				!reflect.DeepEqual(afterHistory.Named, beforeHistory.Named) {
				t.Errorf("mark changed the history, not just the marks")
			}
			if _, ok := afterHistory.Marks["m"]; !ok || len(afterHistory.Marks) != len(beforeHistory.Marks)+1 {
				t.Errorf("marks = %v, want m added", afterHistory.Marks)
			}
		default:
			if !bytes.Equal(before, after) {
				t.Errorf("%s %v wrote the state file:\nbefore %s\nafter  %s", call.tool, call.args, before, after)
			}
		}
	}
}

// Nothing in the flag-on paths is reached from a tool that names one
// resource: its answer is about that resource, not a listing to spend.
func TestWriteListingsSingleTargetDiagnoseSavesNothing(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{}, brokenDeployment("api", "prod"))
	deps.WriteListings = true
	var out diagnoseResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "diagnose", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)
	if len(out.Diagnosis.Resources) != 1 || out.Diagnosis.Resources[0].Index != 0 {
		t.Errorf("resources = %+v, want api with no index", out.Diagnosis.Resources)
	}
	if _, err := deps.State.Load(); err == nil {
		t.Error("a single-target diagnose saved a listing")
	}
}

// An -A table whose rows name no namespace is one kx get prints unnumbered
// and does not save: an index into it would resolve in whatever namespace the
// user stands in. list_resources follows it — no save, and no indexes.
func TestWriteListingsUnplacedAllNamespacesListingIsNotSaved(t *testing.T) {
	deps := writingDeps(t, &recordingKubectl{output: podsOutput})
	var out indexedListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "pods", "allNamespaces": true}), &out)
	if len(out.Resources) == 0 {
		t.Fatal("no resources returned")
	}
	for _, row := range out.Resources {
		if row.Index != 0 {
			t.Errorf("%s carries index %d from a listing that was not saved", row.Name, row.Index)
		}
	}
	if _, err := deps.State.Load(); err == nil {
		t.Error("an unplaced -A listing was saved")
	}
}

// The flag is kx mcp's own, off by default, and reaches the deps the server
// is built from.
func TestWriteListingsFlagIsRegisteredAndReachesTheDeps(t *testing.T) {
	command := newMCPCommand(Services{}, "1.2.3")
	flag := command.Flags().Lookup("write-listings")
	if flag == nil {
		t.Fatal("kx mcp has no --write-listings flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("default = %s, want false", flag.DefValue)
	}
	if want := "Save the agent's listings to your kx history, tagged as agent-made, so their indexes work in your terminal"; flag.Usage != want {
		t.Errorf("usage = %q, want %q", flag.Usage, want)
	}
	services := Services{State: &state.Service{}}
	if liveMCPDeps(services, false).WriteListings || !liveMCPDeps(services, true).WriteListings {
		t.Error("liveMCPDeps does not carry the flag")
	}
}
