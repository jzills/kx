// The MCP server: kx's analysis — ranked diagnostics, ownership trees, marks —
// served to AI agents over the Model Context Protocol.
//
// A target names one resource by kind and name, by a mark, or by an index — a
// row number from the user's current kx listing. An index read is always on:
// it is read-only, resolved live against whatever the user's terminal has
// open, and never echoed back as a number, so an agent copies the name
// forward rather than the digit.
//
// By default nothing here writes the history stack. Started with
// --write-listings, the listing tools — list_resources, the diagnose sweep,
// tree and top — save their listings as the CLI commands they mirror would,
// tagged SourceMCP, and return each row's index in them: the numbers an agent
// shows are then ones the user can spend in kx, and kx's destructive confirms
// say whose listing they came from.
package cli

import (
	"context"
	"sync"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/discovery"
	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/scanner"
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
	// Scanner runs the vulnerability scanner binaries for scan.
	Scanner scanner.Service
	// Discovery, when set, keeps the kind-shorthand source in step with the
	// live context. Nil leaves whatever source is installed alone — tests
	// leave it nil so they never read the ambient kubeconfig's cache.
	Discovery *mcpDiscovery
	// WriteListings is kx mcp --write-listings: the listing tools save what
	// they list to the user's history, tagged as the agent's, and return the
	// indexes. Off, the server writes nothing but marks. See listingSave.
	WriteListings bool

	// mu serialises tool calls; locked takes it, and nothing else does.
	// Every tool runs wholly under it except scan, which holds it only while
	// resolving images and releases it for the scanners themselves. The SDK
	// runs each request on its own goroutine, and kx was built as a CLI that
	// does one thing at a time:
	//
	//   - mark checks that a name is free and then load-modify-saves the
	//     state file, so two concurrent marks lost each other's writes, and
	//     two with one name both passed the check and moved the mark.
	//   - internal/discovery swaps apimachinery's process-wide error handlers
	//     while it reads the cache (withUnhandledErrorsSuppressed), which is
	//     documented unsafe beside concurrent client-go use. Behind this lock
	//     no other call's client-go work runs while it does.
	//
	// A pointer, so the value-receiver handlers all share the one lock.
	mu *sync.Mutex

	// scanSlots admits one scan's scanners at a time: capacity 1. scan runs
	// its scanners outside mu, and scanWorkers is sized for one machine's
	// memory — two agents sweeping at once must not run two pools. Taken
	// only after mu is released, so a queued scan never holds up other tools.
	scanSlots chan struct{}
}

// mcpDiscovery rebuilds the kind-shorthand source when the context changes.
//
// cmd/kx/main.go installs one discovery.Source, which reads kubectl's cache
// once per process — right for a command, stale for a server: after a switch
// it would go on resolving a CRD's shorthand, and deciding its scope, from
// the old cluster's cache. A fresh Source is lazy, so replacing it costs
// nothing until a spelling kx doesn't know is looked up.
type mcpDiscovery struct {
	New     func() kinds.ShorthandSource
	context string
	seen    bool
}

// refresh installs a fresh source if the context moved since the last call.
// The first call only records the context: the source main installed was
// built for it and has not been read yet.
func (m *mcpDiscovery) refresh(current string) {
	if m.seen && current != m.context {
		kinds.SetShorthandSource(m.New())
	}
	m.context, m.seen = current, true
}

// locked runs fn alone, with the discovery source current. It is the one
// place mu is taken — serialized is built on it, and scan calls it directly
// for its resolve phase only (see registerMCPTools).
func (d mcpDeps) locked(fn func() error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Discovery != nil {
		d.Discovery.refresh(d.Kubectl.CurrentContext())
	}
	return fn()
}

// serialized wraps a tool handler so the whole of it runs under locked. Every
// tool but scan is registered through it — see mcpDeps.mu.
func serialized[In, Out any](d mcpDeps, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var (
			result *mcp.CallToolResult
			out    Out
		)
		err := d.locked(func() error {
			var err error
			result, out, err = handler(ctx, req, in)
			return err
		})
		return result, out, err
	}
}

// listingSave is the Save a listing tool hands the CLI command it reuses.
//
// Off, it is discardListing: the server prints no numbers, and saving would
// move the user's own indexes. On, it saves through the user's state service —
// cursor dedupe and per-kind slots exactly as the CLI command would get them —
// with the entry tagged as the agent's, so kx state and the destructive
// confirms can say so.
//
// context is the one the handler read once, before building a client or
// running kubectl, and the entry is stamped with it rather than left for
// Save's live stamp. A sweep or walk takes time; a user who switches context
// while it runs would otherwise have cluster A's rows filed under B, the
// context check would pass in B, and `kx delete 3` would act on a same-named
// resource there. Save's stamp leaves a preset Context alone.
func (d mcpDeps) listingSave(context string) func(state.State) error {
	if !d.WriteListings {
		return discardListing
	}
	return func(entry state.State) error {
		entry.Source = state.SourceMCP
		entry.Context = context
		return d.State.Save(entry)
	}
}

// listingWriter is listingSave for the CLI commands (TopCommand) that take a
// StateWriter rather than a bare Save callable.
func (d mcpDeps) listingWriter(context string) StateWriter {
	return saveFunc(d.listingSave(context))
}

// saveFunc adapts a Save callable to StateWriter.
type saveFunc func(state.State) error

func (f saveFunc) Save(entry state.State) error { return f(entry) }

// indexed says whether a listing tool's output carries indexes: only when its
// listing was saved, since a number that names no saved row would be read
// against whatever listing the user has open.
func (d mcpDeps) indexed() bool {
	return d.WriteListings
}

// silentStatus is the Status the CLI commands a tool reuses are built with.
// Their spinners draw on the terminal, and a server's stdout is the protocol.
func silentStatus(string) func() { return func() {} }

// liveMCPDeps builds the production dependencies with every cache off.
//
// kubectl.Exec{} rather than kubectl.New(): a nil context cache reads the
// kubeconfig on every CurrentContext call. The state service shares the CLI's
// file — marks are the point of sharing it — but stamps and checks context
// through the uncached reader.
func liveMCPDeps(services Services, writeListings bool) mcpDeps {
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
		Scanner:    services.scannerService(),
		Discovery:  &mcpDiscovery{New: func() kinds.ShorthandSource { return discovery.NewSource() }},
		mu:         &sync.Mutex{},
		scanSlots:  make(chan struct{}, 1),

		WriteListings: writeListings,
	}
}

// mcpInstructions is what a client shows its model about the server as a whole.
const mcpInstructions = "kx reads a Kubernetes cluster through the caller's kubeconfig. " +
	"Start with diagnose (no target) to find what is unhealthy, then diagnose, tree, events, " +
	"logs, top, get_yaml or scan a specific resource for more evidence. list_resources lists a " +
	"kind by name. Resources are named by kind/name/namespace, by a kx mark — a name the " +
	"user pinned to a resource (list_marks shows them) — or by an index: a row number from the " +
	"user's current kx listing, e.g. the 3 in 'diagnose 3'. Confirm the resolved name back to " +
	"the user before acting on an index. mark pins one. If results carry an index, that is the " +
	"row's number in the user's kx history, and the user can spend it in kx: that listing is now " +
	"the user's current one, so an index given to you after it refers to your listing, not an " +
	"earlier one of theirs. Every tool is " +
	"read-only against the cluster; anything written goes only to kx's local state."

func newMCPServer(deps mcpDeps, version string) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "kx", Title: "kx", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions},
	)
	registerMCPTools(server, deps)
	return server
}

func newMCPCommand(services Services, version string) *cobra.Command {
	command := &cobra.Command{
		Use:   "mcp",
		Short: "Serve kx's diagnostics, ownership trees, evidence and marks to AI agents over MCP (stdio).",
		Long: "Runs a Model Context Protocol server on stdin/stdout, for an MCP client — " +
			"Claude Code, an IDE, an agent framework — to start as a subprocess.\n\n" +
			"Ten tools: list_marks and mark for kx's marks; list_resources to list a kind; " +
			"diagnose and tree for a resource's health and ownership; events and logs for what " +
			"happened; top for current usage; get_yaml for its manifest (Secrets redacted); and " +
			"scan for image CVEs.\n\n" +
			"Every tool is read-only against the cluster. Resources are named by kind and name, " +
			"by a mark, or by an index — a row number from your terminal's current listing, read " +
			"live. By default the one thing it writes is a new mark, and " +
			"it refuses to move one you already set.\n\n" +
			"The server follows your kubeconfig live: switch context and the next call reads " +
			"the new cluster, and says so in its result.\n\n" +
			"With --write-listings, what the agent lists — list_resources, a diagnose sweep, " +
			"tree and top — is saved to your kx history as `kx get`, `kx diag`, `kx tree` and " +
			"`kx top` would save it, and each row carries its index, so a number the agent " +
			"quotes works in your terminal. Those listings are tagged: kx state says 'via kx mcp', " +
			"and kx delete and kx drain say an index came from one before they act.",
		Example: "  claude mcp add kx -- kx mcp\n" +
			"  claude mcp add kx -- kx mcp --write-listings",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			writeListings, _ := cmd.Flags().GetBool("write-listings")
			// Built here rather than in NewRoot: tests construct the root with
			// a zero Services, whose State is nil.
			server := newMCPServer(liveMCPDeps(services, writeListings), version)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
	// No environment variable: the flag sits in the MCP client's server
	// config, where it is visible and deliberate, and a stray export must not
	// change what an agent can do to your state.
	command.Flags().Bool("write-listings", false,
		"Save the agent's listings to your kx history, tagged as agent-made, so their indexes work in your terminal")
	return command
}
