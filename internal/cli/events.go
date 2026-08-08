package cli

import (
	"context"
	"strings"

	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
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
		Example: "  kx events 1\n  kx events 1 2",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes("indexes", args)
			if err != nil {
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
		Use:   "top [resource] [kubectl flags]",
		Short: "List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits.",
		Long: "Lists pod or node CPU and memory usage with kubectl top, assigns\n" +
			"indexes, and shows CPU%/MEM% — computed against each pod's limits\n" +
			"for pods, native to kubectl for nodes.",
		Example:            "  kx top\n  kx top nodes\n  kx top -m web\n  kx top --no-limits",
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

			// A leading non-flag token names the resource type, mirroring
			// how `kx get`/`kx <kind>` resolve kind shorthands. Anything
			// that doesn't resolve to Node (including no token at all)
			// falls through to the pods path unchanged, exactly as it
			// worked before this resource argument existed.
			nodes := false
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") &&
				kinds.Normalize(rest[0]) == kinds.Node {
				nodes = true
				rest = rest[1:]
			}

			command := TopCommand{
				Kubectl: services.Kubectl, State: services.State, Index: services.Index,
			}
			resourceLabel := "pods"
			var output, namespace string
			if nodes {
				resourceLabel = "nodes"
				output, namespace, err = command.ExecuteNodes(rest)
			} else {
				output, namespace, err = command.Execute(match, rest, noLimits)
			}
			if err != nil {
				return err
			}
			render.IndexedTable(output, resourceLabel, namespace, "")
			return nil
		},
	}
	// Registered so they appear in the command's help; parsing is by hand.
	cmd.Flags().StringP("match", "m", "", "Match by name (substring, case-insensitive)")
	cmd.Flags().Bool("no-limits", false,
		"Skip the CPU%/MEM% columns (one fewer kubectl call)")
	// Pure kubectl passthrough, parsed by hand like every other flag here —
	// registered only so they appear in --help instead of vanishing.
	cmd.Flags().StringP("namespace", "n", "", "Namespace to list from; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false, "List across every namespace; results are not indexed")
	return cmd
}
