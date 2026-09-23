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
