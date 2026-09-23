package cli

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
// may ask kubectl only to get. This is the read-only promise as a test.
func TestMCPToolsOnlyEverGet(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput, namespace: "prod"}
	deps := mcpDiagDeps(t, kube, brokenDeployment("api", "prod"))
	session := connectMCP(t, deps)
	target := map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"list_marks", map[string]any{}},
		{"list_resources", map[string]any{"kind": "pods"}},
		{"list_resources", map[string]any{"kind": "pods", "allNamespaces": true}},
		{"diagnose", map[string]any{}},
		{"diagnose", map[string]any{"target": target}},
		{"tree", map[string]any{"target": target}},
		{"mark", map[string]any{"name": "m", "target": target}},
	} {
		callTool(t, session, call.tool, call.args)
	}
	for _, args := range kube.runs {
		if len(args) == 0 || args[0] != "get" {
			t.Errorf("kubectl %v — only get is allowed", args)
		}
	}
	if len(kube.interactive) != 0 {
		t.Errorf("interactive kubectl calls: %v", kube.interactive)
	}
}
