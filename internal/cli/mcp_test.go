package cli

import (
	"context"
	"testing"

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
