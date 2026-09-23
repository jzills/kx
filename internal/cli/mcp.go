// The MCP server: kx's analysis — ranked diagnostics, ownership trees, marks —
// served to AI agents over the Model Context Protocol.
//
// Deliberately not the index workflow. Numbers save a person typing names; an
// agent copies names perfectly, and an agent spending the user's indexes would
// be acting on whatever listing the user has open. So nothing here takes or
// returns an index, and nothing here writes the history stack.
package cli

import (
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

// mcpDeps are what the server's tools are built from.
//
// Not Services, because Services caches for the life of the process: the
// kubectl service memoises the current context and kubernetesClient builds one
// client-go client. A command lives for a second; a server lives for a session,
// and a user who switches context halfway through it would have the server
// reading one cluster through client-go while kubectl reads another.
type mcpDeps struct {
	Kubectl kubectl.Service
	State   *state.Service
	// Kubernetes builds a fresh API client, called once per tool call.
	Kubernetes func() (kubernetes.Interface, error)
	Config     config.Config
}

// liveMCPDeps builds the production dependencies with every cache off.
//
// kubectl.Exec{} rather than kubectl.New(): a nil context cache reads the
// kubeconfig on every CurrentContext call. The state service shares the CLI's
// file — marks are the point of sharing it — but stamps and checks context
// through the uncached reader.
func liveMCPDeps(services Services) mcpDeps {
	kube := kubectl.Exec{}
	return mcpDeps{
		Kubectl: kube,
		State: &state.Service{
			MaxHistory: services.State.MaxHistory,
			Path:       services.State.Path,
			Context:    kube.CurrentContext,
		},
		Kubernetes: func() (kubernetes.Interface, error) { return k8s.Client() },
		Config:     services.Config,
	}
}

// mcpInstructions is what a client shows its model about the server as a whole.
const mcpInstructions = "kx reads a Kubernetes cluster through the caller's kubeconfig. " +
	"Start with diagnose (no target) to find what is unhealthy, then diagnose or tree a " +
	"specific resource. Resources are named by kind/name/namespace, or by a kx mark — a " +
	"name the user pinned to a resource (list_marks shows them). Every tool is read-only " +
	"against the cluster; mark only records a name in kx's local state."

func newMCPServer(deps mcpDeps, version string) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "kx", Title: "kx", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions},
	)
	registerMCPTools(server, deps)
	return server
}

func newMCPCommand(services Services, version string) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve kx's diagnostics, ownership trees and marks to AI agents over MCP (stdio).",
		Long: "Runs a Model Context Protocol server on stdin/stdout, for an MCP client — " +
			"Claude Code, an IDE, an agent framework — to start as a subprocess.\n\n" +
			"Every tool is read-only against the cluster. Resources are named by kind and name " +
			"or by a mark, never by index, and the server never touches the listing your " +
			"terminal's indexes resolve against. The one thing it writes is a new mark, and it " +
			"refuses to move one you already set.\n\n" +
			"The server follows your kubeconfig live: switch context and the next call reads " +
			"the new cluster, and says so in its result.",
		Example: "  claude mcp add kx -- kx mcp",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Built here rather than in NewRoot: tests construct the root with
			// a zero Services, whose State is nil.
			server := newMCPServer(liveMCPDeps(services), version)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}
