package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/web"
	"github.com/spf13/cobra"
)

// EventsCommand shows Kubernetes events for an indexed resource.
type EventsCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	Events  events.Service
}

func (c EventsCommand) Execute(ctx context.Context, index int) ([]events.Row, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return nil, err
	}
	all, err := c.Events.Get(ctx, namespace)
	if err != nil {
		return nil, err
	}
	filtered := c.Events.Filter(all, name, kind)
	if len(filtered) == 0 {
		// Deleted resources keep their events for about an hour, so only an
		// empty result is worth a staleness check — a resource with events is
		// evidently still known to the cluster.
		if err := ensureExists(c.Kubectl, kind, name, namespace); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return events.Rows(filtered), nil
}

func newEventsCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:     "events <index>...",
		Short:   "Show Kubernetes events for one or more indexed resources.",
		Example: "  kx events 1\n  kx events 1 2\n  kx events 1..3\n  kx events 3..",
		Args:    minArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes(services.State, "indexes", args)
			if err != nil {
				return err
			}
			if err := validateIndexes(services.State, indexes); err != nil {
				return err
			}
			client, err := services.Kubernetes()
			if err != nil {
				return err
			}
			command := EventsCommand{
				Kubectl: services.Kubectl,
				State:   services.State,
				Events:  events.APIService{Client: client},
			}
			for position, index := range indexes {
				name, namespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				stop := render.Status("fetching events")
				rows, err := command.Execute(cmd.Context(), index)
				stop()
				if err != nil {
					return err
				}
				extra := ""
				if len(rows) > 0 {
					extra = itemCount(len(rows))
				}
				if position > 0 {
					render.Blank()
				}
				render.Banner(string(kind), name, namespace, extra)
				render.EventsTable(rows)
			}
			return nil
		},
	}
}

func newTopCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:        "top [resource] [kubectl flags]",
		SuggestFor: []string{"usage", "metrics", "stats"},
		Short:      "List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits.",
		Long:       "Lists pod or node CPU and memory usage with kubectl top, assigns indexes, and shows CPU%/MEM% — computed against each pod's limits for pods, native to kubectl for nodes.",
		Example:    "  kx top\n  kx top nodes\n  kx top -m web\n  kx top --no-limits",
		// `resource` means something narrower here than it does to kx get.
		Annotations:        map[string]string{"arg.resource": "pods (the default) or nodes"},
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			match, rest, err := extractString(rest, "--match", "-m")
			if err != nil {
				return err
			}
			noLimits, rest := extractBool(rest, "--no-limits")
			asJSON, rest := extractBool(rest, "--json")
			html, rest := extractBool(rest, "--html")
			noOpen, rest := extractBool(rest, "--no-open")
			out, rest, err := extractString(rest, "--out", "")
			if err != nil {
				return err
			}
			hasPort := hasFlag(rest, "--port", "")
			portText, rest, err := extractString(rest, "--port", "")
			if err != nil {
				return err
			}
			port := 0
			if portText != "" {
				if port, err = strconv.Atoi(portText); err != nil {
					return fmt.Errorf(
						"Invalid value for '--port': '%s' is not a valid int.", portText)
				}
			}
			wantsHTML := impliedHTML(html, out)
			htmlOpts := htmlOptions{Enabled: wantsHTML, Port: port, NoOpen: noOpen, Out: out}
			if err := htmlOpts.validate(hasPort, noOpen); err != nil {
				return err
			}
			if asJSON && wantsHTML {
				return fmt.Errorf(
					"'--json' cannot be combined with '%s' — one is for a "+
						"machine and the other for a browser.", htmlFlagName(html))
			}

			// A leading non-flag token names the resource type, mirroring
			// how `kx get`/`kx <kind>` resolve kind shorthands. A
			// recognized Pod or Node shorthand is consumed either way —
			// `kx top pods` must strip "pods" the same way `kx top nodes`
			// strips "nodes", or it leaks through to kubectl as a pod-name
			// filter instead of a resource type. Anything else (including
			// no token at all) falls through to the pods path completely
			// unchanged, exactly as it worked before this argument existed.
			nodes := false
			topArg := ""
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				switch kinds.Normalize(rest[0]) {
				case kinds.Node:
					nodes = true
					topArg = "nodes"
					rest = rest[1:]
				case kinds.Pod:
					rest = rest[1:]
				}
			}

			// kubectl rejects -A on `top node` with its own error, naming its
			// own command; -n it accepts and ignores. Both are refused here
			// instead, in kx's voice and before kubectl is spawned, because a
			// Node has no namespace either flag could be talking about. kx top
			// pods is untouched — see clusterScopedScopeError.
			if nodes {
				if flag := scopeFlagIn(rest); flag != "" {
					return clusterScopedScopeError(flag, "nodes")
				}
				// --no-limits skips the second kubectl call that fetches each
				// pod's limits, so the percentage columns can be computed.
				// kubectl reports a node's own CPU%/MEM% against its capacity,
				// in the same table — there is no extra call to skip and no
				// column that goes away, so the flag did nothing at all here.
				if noLimits {
					return errors.New(
						"'--no-limits' cannot be combined with 'nodes' — kubectl " +
							"reports a node's CPU% and MEM% against its capacity, " +
							"in the same call, so there are no limits to skip.")
				}
			}

			command := TopCommand{
				Kubectl: services.Kubectl, State: services.State, Index: services.Index,
			}
			resourceLabel := "pods"
			scopedAllNamespaces := false
			var output index.Table
			var namespace string
			if nodes {
				resourceLabel = "nodes"
				output, namespace, err = command.ExecuteNodes(match, rest)
			} else {
				scopedAllNamespaces = allNamespaces(rest)
				output, namespace, err = command.Execute(match, rest, noLimits)
				if scopedAllNamespaces {
					// Matches kx get -A's own caption override (getbody.go):
					// many namespaces span the listing, so there is no
					// single one to name.
					namespace = render.AllNamespaces
				}
			}
			if err != nil {
				return err
			}
			if asJSON {
				// namespace is the caption's by this point: an -A listing has
				// had it overwritten with the words "all namespaces" just
				// above, and putting that in a document is what kx scan's
				// "scope" used to do — indistinguishable from a namespace
				// genuinely called that. The boolean carries the scope instead.
				//
				// A node listing needs no guard of its own: ExecuteNodes
				// returns an empty namespace because a Node is cluster-scoped,
				// so there is nothing here to blank.
				subject := scanSubject{
					Namespace: namespace, AllNamespaces: scopedAllNamespaces,
				}
				if scopedAllNamespaces {
					subject.Namespace = ""
				}
				document, err := topJSON(subject, resourceLabel, topPageRows(output))
				if err != nil {
					return err
				}
				render.Raw(document)
				return nil
			}
			render.IndexedTable(output, resourceLabel, namespace)
			if !htmlOpts.Enabled {
				return nil
			}

			label := kinds.PluralDisplay(resourceLabel)
			meta, err := pageMeta(services.Config.Theme, "top · "+label,
				invocation("top", topArg, scopeArgs(namespace, scopedAllNamespaces), portFlag(port)))
			if err != nil {
				return err
			}
			page, err := web.RenderTop(web.TopPage{
				Meta: meta, Scope: scopeCaption(label, namespace), Rows: topPageRows(output),
			})
			if err != nil {
				return err
			}
			return deliverPage(cmd.Context(), page, htmlOpts)
		},
	}
	// Registered so they appear in the command's help; parsing is by hand.
	cmd.Flags().StringP("match", "m", "", "Match by name (substring, case-insensitive)")
	cmd.Flags().Bool("no-limits", false,
		"Skip the CPU%/MEM% columns (one fewer kubectl call)")
	cmd.Flags().Bool("json", false, "Print the listing as JSON instead of a table")
	cmd.Flags().Bool("html", false, "Render the listing as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0, "Port to serve --html on (random free port by default)")
	cmd.Flags().Bool("no-open", false, "Don't open a browser automatically with --html")
	cmd.Flags().String("out", "", "Write the HTML report to this file instead of serving it in a browser")
	// Pure kubectl passthrough, parsed by hand like every other flag here —
	// registered only so they appear in --help instead of vanishing.
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to list pods from; defaults to the current namespace. Not for nodes, which are not in a namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"List pods across every namespace; each row is indexed and carries its own namespace. Not for nodes, which are not in a namespace")
	return cmd
}
