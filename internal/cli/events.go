package cli

import (
	"context"

	"github.com/jzills/kx/internal/events"
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
		Use:   "top [kubectl flags]",
		Short: "List CPU/memory usage for pods in the current namespace and assign index numbers, like kx get; shows usage as a percent of each pod's resource limits unless --no-limits.",
		Long: "Lists pod CPU and memory usage with kubectl top, assigns indexes,\n" +
			"and adds CPU%/MEM% columns computed against each pod's limits.",
		Example:            "  kx top\n  kx top -m web\n  kx top --no-limits",
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

			output, err := TopCommand{
				Kubectl: services.Kubectl, State: services.State, Index: services.Index,
			}.Execute(match, rest, noLimits)
			if err != nil {
				return err
			}
			namespace := extractNamespace(rest)
			if namespace == "" {
				namespace = services.Kubectl.CurrentNamespace()
			}
			render.IndexedTable(output, "pods", namespace, "")
			return nil
		},
	}
	// Registered so they appear in the command's help; parsing is by hand.
	cmd.Flags().StringP("match", "m", "", "Match by name (substring, case-insensitive)")
	cmd.Flags().Bool("no-limits", false,
		"Skip the CPU%/MEM% columns (one fewer kubectl call)")
	return cmd
}
