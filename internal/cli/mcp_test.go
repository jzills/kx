package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// connectMCP starts the server over an in-memory transport and returns a
// connected client session — the whole protocol, without a subprocess.
func connectMCP(t *testing.T, deps mcpDeps) *mcp.ClientSession {
	t.Helper()
	return connectMCPWith(t, deps, nil)
}

// connectMCPWith is connectMCP with client options — a progress handler, say.
func connectMCPWith(t *testing.T, deps mcpDeps, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(deps, "1.2.3").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, options)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// A client lists servers by the name and version they introduce themselves
// with, so both have to be kx's own rather than the SDK's defaults.
func TestMCPServerIntroducesItselfAsKx(t *testing.T) {
	session := connectMCP(t, mcpDeps{})
	info := session.InitializeResult().ServerInfo
	if info.Name != "kx" || info.Version != "1.2.3" {
		t.Errorf("serverInfo = %s %s, want kx 1.2.3", info.Name, info.Version)
	}
}

// The tool list is a public surface: an agent's prompts and a user's allow
// rules name these tools. Changing it should be a decision, not a side effect.
func TestMCPToolSurface(t *testing.T) {
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{}))
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{ // name → read-only
		"list_marks": true, "mark": false, "list_resources": true, "diagnose": true, "tree": true,
		"events": true, "logs": true, "top": true, "get_yaml": true, "scan": true,
	}
	if len(result.Tools) != len(want) {
		t.Errorf("%d tools, want %d", len(result.Tools), len(want))
	}
	for _, tool := range result.Tools {
		readOnly, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s: annotations %+v, want readOnlyHint %v", tool.Name, tool.Annotations, readOnly)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
}

// podsAllNamespacesOutput is kubectl get pods -A: a NAMESPACE column places
// every row, which is what lets --write-listings save it.
const podsAllNamespacesOutput = "NAMESPACE   NAME      READY   STATUS    RESTARTS   AGE\n" +
	"prod        api-pod   1/1     Running   0          1d\n"

// readOnlyCall is one call the read-only guards drive through the protocol.
type readOnlyCall struct {
	tool string
	args map[string]any
	// kubectlRuns is how many kubectl.Run calls this exact call must make
	// — 0 for anything that reads through client-go instead — so a tool
	// that stops calling kubectl (or client-go) altogether cannot pass by
	// leaving an empty, technically-compliant runs list.
	kubectlRuns   int
	kubectlProbes int
	// scannerCalls is how many scanner.Service calls it must make: the
	// engine's preflight, then one summary per image.
	scannerCalls int
	// lists says the call is a listing --write-listings saves: list_resources,
	// the diagnose sweep, tree and top. Every other call must leave the history
	// stack alone whatever the flag says.
	lists bool
}

// readOnlyFixture is every tool with every argument shape it takes, and the
// fakes that answer them — shared by the flag-off guard below and the
// flag-on one in mcp_listings_test.go, so the two cannot drift onto different
// sets of calls.
func readOnlyFixture(t *testing.T) (mcpDeps, *recordingKubectl, *fake.Clientset, *fakeScanner, []readOnlyCall) {
	t.Helper()
	kube := &recordingKubectl{
		namespace: "prod",
		outputs: []string{
			podsOutput,              // list_resources pods
			podsAllNamespacesOutput, // list_resources pods -A
			"deployment.apps/api\n", // mark's existence check
			"line1\nline2\n",        // logs on the Pod
			`{"spec":{"selector":{"matchLabels":{"app":"web"}}}}`, // logs' selector read
			"[api-pod] prefixed log line\n",                       // logs on the Deployment
			topPodsFixture,                                        // top pods
			podsJSON,                                              // top pods' limits lookup
			nodesOutput,                                           // top nodes
			"apiVersion: v1\nkind: Deployment\nmetadata:\n  name: api\n", // get_yaml
			secretManifest,                  // get_yaml on a Secret
			workloadJSON("api:v1", "db:v1"), // scan's image read of the Deployment
			`{"items":[` + workloadJSON("api:v1", "web:v1") + `]}`, // scan's namespace sweep
		},
	}
	deployment := brokenDeployment("api", "prod")
	// A Namespace object (graph.Builder.Namespaces lists actual Namespace
	// objects, not namespaces inferred from a workload's metadata — see
	// treeFixture) plus a ReplicaSet/Pod chain owned by the Deployment, so
	// every call — including both allNamespaces sweeps — has something to
	// find and succeeds rather than erroring on an empty fixture.
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "api-rs", Namespace: "prod", UID: types.UID("api-rs"),
		OwnerReferences: []metav1.OwnerReference{{UID: types.UID("api")}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-pod", Namespace: "prod", UID: types.UID("api-pod"),
		OwnerReferences: []metav1.OwnerReference{{UID: types.UID("api-rs")}},
	}}
	client := fake.NewSimpleClientset(namespace, deployment, replicaSet, pod)
	deps := mcpTestDeps(t, kube)
	deps.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	deps.Config.Engine = "trivy"
	scans := &fakeScanner{captures: []captured{
		{image: "api:v1", stdout: "{}"}, {image: "db:v1", stdout: "{}"}, {image: "web:v1", stdout: "{}"},
	}}
	deps.Scanner = scans

	target := map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}
	podTarget := map[string]any{"kind": "pods", "name": "api-pod", "namespace": "prod"}
	secretTarget := map[string]any{"kind": "secret", "name": "creds", "namespace": "prod"}
	calls := []readOnlyCall{
		{"list_marks", map[string]any{}, 0, 0, 0, false},
		{"list_resources", map[string]any{"kind": "pods"}, 1, 0, 0, true},
		{"list_resources", map[string]any{"kind": "pods", "allNamespaces": true}, 1, 0, 0, true},
		{"diagnose", map[string]any{}, 0, 0, 0, true},
		{"diagnose", map[string]any{"allNamespaces": true}, 0, 0, 0, true},
		{"diagnose", map[string]any{"target": target}, 0, 0, 0, false},
		{"tree", map[string]any{}, 0, 0, 0, true},
		{"tree", map[string]any{"target": target}, 0, 0, 0, true},
		{"tree", map[string]any{"allNamespaces": true}, 0, 0, 0, true},
		{"mark", map[string]any{"name": "m", "target": target}, 1, 0, 0, false},
		// No matching events in the fixture, so this exercises the
		// staleness probe rather than a kubectl.Run.
		{"events", map[string]any{"target": target}, 0, 1, 0, false},
		{"logs", map[string]any{"target": podTarget}, 1, 0, 0, false},
		{"logs", map[string]any{"target": target}, 2, 0, 0, false},
		{"top", map[string]any{}, 2, 1, 0, true},
		{"top", map[string]any{"nodes": true}, 1, 1, 0, true},
		{"get_yaml", map[string]any{"target": target}, 1, 0, 0, false},
		{"get_yaml", map[string]any{"target": secretTarget}, 1, 0, 0, false},
		{"scan", map[string]any{"target": target}, 1, 0, 3, false},
		{"scan", map[string]any{"namespace": "prod", "engine": "trivy"}, 1, 0, 3, false},
	}
	return deps, kube, client, scans, calls
}

// seedUserListing saves the listing a user's own `kx get configmaps -n other`
// would, so a guard has something of the user's for a stray save to disturb.
// A query no fixture call repeats: seeded with one a call does repeat, a
// leaked untagged save of that call deduped onto the seed and left the file
// byte-identical.
func seedUserListing(t *testing.T, deps mcpDeps) {
	t.Helper()
	const configMaps = "NAME       DATA   AGE\nsettings   1      1d\n"
	if err := deps.State.Save(getListing("configmaps", "", []string{"-n", "other"}, "other",
		index.Service{}.Add(configMaps).Entries)); err != nil {
		t.Fatal(err)
	}
}

// Every tool, driven through the protocol with every argument shape it takes,
// may ask kubectl only to get, log or top, may ask client-go only to read
// (get/list/watch, never a write verb), and every one of those calls must
// actually succeed — so a tool that started erroring out before it ever
// reached kubectl or client-go couldn't quietly pass this by making the
// recorded calls list look short and clean. This is the read-only promise as
// a test, covering every path that reads a cluster: kubectl (list_resources,
// mark, events, logs, top, get_yaml, scan) and client-go (diagnose, tree) —
// and scan's scanner, which may only preflight and summarise.
//
// With --write-listings off, which is the default, it is the local-state
// promise too: the state file ends byte-for-byte what the user's own listing
// plus the one mark makes it, and no result carries an index.
func TestMCPToolsOnlyRead(t *testing.T) {
	deps, kube, client, scans, calls := readOnlyFixture(t)
	seedUserListing(t, deps)
	// What the file must end as: the user's listing, untouched, beside the
	// one mark the calls make — written by the state service itself onto a
	// copy, so the comparison is against kx's own encoding rather than a
	// hand-written expectation of it.
	want := &state.Service{MaxHistory: deps.State.MaxHistory, Path: filepath.Join(t.TempDir(), "want.json")}
	seeded, err := os.ReadFile(deps.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want.Path, seeded, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := want.SaveMark("m", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Deployment, Namespace: "prod"},
		Context:  kube.CurrentContext(),
	}); err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, deps)

	wantKubectlRuns, wantKubectlProbes, wantScannerCalls := 0, 0, 0
	for _, call := range calls {
		result := callTool(t, session, call.tool, call.args)
		if result.IsError {
			t.Fatalf("%s %v failed: %s", call.tool, call.args, toolText(result))
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"index"`)) {
			t.Errorf("%s %v: output carries an index with --write-listings off: %s", call.tool, call.args, raw)
		}
		wantKubectlRuns += call.kubectlRuns
		wantKubectlProbes += call.kubectlProbes
		wantScannerCalls += call.scannerCalls
	}

	got, err := os.ReadFile(deps.State.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := os.ReadFile(want.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantBytes) {
		t.Errorf("state file changed beyond the mark:\ngot  %s\nwant %s", got, wantBytes)
	}

	if len(kube.runs) != wantKubectlRuns {
		t.Errorf("kubectl ran %d times, want %d: %v", len(kube.runs), wantKubectlRuns, kube.runs)
	}
	for _, args := range kube.runs {
		if len(args) == 0 || (args[0] != "get" && args[0] != "logs" && args[0] != "top") {
			t.Errorf("kubectl %v — only get, logs and top are allowed", args)
		}
	}
	if len(kube.probes) != wantKubectlProbes {
		t.Errorf("kubectl probed %d times, want %d: %v", len(kube.probes), wantKubectlProbes, kube.probes)
	}
	for _, args := range kube.probes {
		if len(args) == 0 || (args[0] != "get" && args[0] != "logs" && args[0] != "top") {
			t.Errorf("kubectl probe %v — only get, logs and top are allowed", args)
		}
	}
	// A scanner is a subprocess too, and pulls from registries: it may only
	// ever be asked whether it is installed, or for its machine-readable
	// summary of one image — never the passthrough argv, which carries
	// caller-supplied flags.
	if len(scans.argv) != wantScannerCalls {
		t.Errorf("scanner ran %d times, want %d: %v", len(scans.argv), wantScannerCalls, scans.argv)
	}
	engine := scanner.Trivy{}
	for _, argv := range scans.argv {
		allowed := slices.Equal(argv, engine.PreflightArgv())
		for _, image := range []string{"api:v1", "db:v1", "web:v1"} {
			allowed = allowed || slices.Equal(argv, engine.SummaryArgv(image))
		}
		if !allowed {
			t.Errorf("scanner %v — only the engine's preflight and summary argv are allowed", argv)
		}
	}
	if len(kube.interactive) != 0 {
		t.Errorf("interactive kubectl calls: %v", kube.interactive)
	}

	actions := client.Actions()
	if len(actions) == 0 {
		t.Fatal("no client-go actions recorded — diagnose and tree read through client-go, so this check saw nothing to verify")
	}
	for _, action := range actions {
		if verb := action.GetVerb(); verb != "get" && verb != "list" && verb != "watch" {
			t.Errorf("client-go %s %s — only get/list/watch are allowed", verb, action.GetResource().Resource)
		}
	}
}

// switchingKubectl reports whatever context the test last switched to.
type switchingKubectl struct {
	recordingKubectl
	context string
}

func (k *switchingKubectl) CurrentContext() string { return k.context }

// mapSource resolves one spelling, so a test can tell which source answered.
type mapSource map[string]kinds.Kind

func (m mapSource) Resolve(spelling string) (kinds.Kind, string, bool) {
	kind, ok := m[spelling]
	return kind, "", ok
}

func (mapSource) Namespaced(kinds.Kind) (bool, bool) { return false, false }

// The discovery source is read once per process, which is right for a
// command and wrong for a session: after a switch, a CRD's shorthand would go
// on resolving from the old cluster's cache. The server rebuilds it when the
// context moves — and only then.
func TestMCPRebuildsKindDiscoveryWhenTheContextSwitches(t *testing.T) {
	kinds.SetShorthandSource(mapSource{"gw": "GatewayFromA"})
	t.Cleanup(func() { kinds.SetShorthandSource(nil) })

	kube := &switchingKubectl{recordingKubectl: recordingKubectl{output: "NAME   AGE\nweb   1d\n"}, context: "a"}
	built := 0
	deps := mcpTestDeps(t, kube)
	deps.Discovery = &mcpDiscovery{New: func() kinds.ShorthandSource {
		built++
		return mapSource{"gw": "GatewayFromB"}
	}}
	session := connectMCP(t, deps)
	listKind := func() string {
		var out listOutput
		decodeStructured(t, callTool(t, session, "list_resources", map[string]any{"kind": "gw"}), &out)
		return out.Kind
	}

	if got := listKind(); got != "GatewayFromA" || built != 0 {
		t.Fatalf("first call: kind %q after %d rebuilds, want GatewayFromA and none", got, built)
	}
	listKind()
	if built != 0 {
		t.Errorf("rebuilt %d times with the context unchanged, want 0", built)
	}
	kube.context = "b"
	if got := listKind(); got != "GatewayFromB" || built != 1 {
		t.Errorf("after switching: kind %q after %d rebuilds, want GatewayFromB and 1", got, built)
	}
}

// The tools' schemas are the contract an agent is prompted with: renaming a
// field, dropping a description or loosening a type changes what every client
// sends. Pinned whole — with each tool's description and the server's
// instructions, which are prompt text too — so any change shows up as a diff
// to review.
// Regenerate with: KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas
func TestMCPToolSchemas(t *testing.T) {
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{}))
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Name         string               `json:"name"`
		Description  string               `json:"description"`
		Annotations  *mcp.ToolAnnotations `json:"annotations"`
		InputSchema  any                  `json:"inputSchema"`
		OutputSchema any                  `json:"outputSchema"`
	}
	tools := make([]schema, 0, len(result.Tools))
	for _, tool := range result.Tools {
		tools = append(tools, schema{tool.Name, tool.Description, tool.Annotations, tool.InputSchema, tool.OutputSchema})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	// Read from the handshake, as a client sees them, rather than from the
	// constant.
	golden := struct {
		Instructions string   `json:"instructions"`
		Tools        []schema `json:"tools"`
	}{session.InitializeResult().Instructions, tools}
	got, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "mcp-tools.golden.json")
	if os.Getenv("KX_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — regenerate with KX_UPDATE_GOLDEN=1", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("tool schemas differ from %s — if the change is intended, regenerate with "+
			"KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas\ngot:\n%s", path, got)
	}
}
