package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jzills/kx/internal/kinds"
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
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(deps, "1.2.3").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
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

// Every tool, driven through the protocol with every argument shape it takes,
// may ask kubectl only to get, may ask client-go only to read (get/list/watch,
// never a write verb), and every one of those calls must actually succeed —
// so a tool that started erroring out before it ever reached kubectl or
// client-go couldn't quietly pass this by making the recorded calls list look
// short and clean. This is the read-only promise as a test, covering both of
// the paths that read a cluster: kubectl (list_resources, mark) and
// client-go (diagnose, tree).
func TestMCPToolsOnlyEverGet(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput, namespace: "prod"}
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
	session := connectMCP(t, deps)

	target := map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}
	calls := []struct {
		tool string
		args map[string]any
		// kubectlRuns is how many kubectl.Run calls this exact call must make
		// — 0 for anything that reads through client-go instead — so a tool
		// that stops calling kubectl (or client-go) altogether cannot pass by
		// leaving an empty, technically-compliant runs list.
		kubectlRuns int
	}{
		{"list_marks", map[string]any{}, 0},
		{"list_resources", map[string]any{"kind": "pods"}, 1},
		{"list_resources", map[string]any{"kind": "pods", "allNamespaces": true}, 1},
		{"diagnose", map[string]any{}, 0},
		{"diagnose", map[string]any{"allNamespaces": true}, 0},
		{"diagnose", map[string]any{"target": target}, 0},
		{"tree", map[string]any{}, 0},
		{"tree", map[string]any{"target": target}, 0},
		{"tree", map[string]any{"allNamespaces": true}, 0},
		{"mark", map[string]any{"name": "m", "target": target}, 1},
	}
	wantKubectlRuns := 0
	for _, call := range calls {
		result := callTool(t, session, call.tool, call.args)
		if result.IsError {
			t.Fatalf("%s %v failed: %s", call.tool, call.args, toolText(result))
		}
		wantKubectlRuns += call.kubectlRuns
	}

	if len(kube.runs) != wantKubectlRuns {
		t.Errorf("kubectl ran %d times, want %d: %v", len(kube.runs), wantKubectlRuns, kube.runs)
	}
	for _, args := range kube.runs {
		if len(args) == 0 || args[0] != "get" {
			t.Errorf("kubectl %v — only get is allowed", args)
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
// sends. Pinned whole, so any change shows up as a diff to review.
// Regenerate with: KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas
func TestMCPToolSchemas(t *testing.T) {
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{}))
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Name         string               `json:"name"`
		Annotations  *mcp.ToolAnnotations `json:"annotations"`
		InputSchema  any                  `json:"inputSchema"`
		OutputSchema any                  `json:"outputSchema"`
	}
	tools := make([]schema, 0, len(result.Tools))
	for _, tool := range result.Tools {
		tools = append(tools, schema{tool.Name, tool.Annotations, tool.InputSchema, tool.OutputSchema})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	got, err := json.MarshalIndent(tools, "", "  ")
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
