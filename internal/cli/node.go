package cli

import (
	"fmt"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/spf13/cobra"
)

// NodeCommand marks an indexed node schedulable or unschedulable.
//
// One struct for both verbs because they are the same operation in opposite
// directions — kubectl spells them as two commands taking the same argument,
// and so does kx.
type NodeCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Verb is "cordon" or "uncordon", passed straight to kubectl.
	Verb string
}

// Execute runs the verb against one indexed node, returning the line to report.
func (c NodeCommand) Execute(index int) (string, error) {
	name, _, kind, err := c.State.Fields(index)
	if err != nil {
		return "", err
	}
	if kind != kinds.Node {
		return "", fmt.Errorf("%s is only supported for nodes.", c.Verb)
	}
	// No -n: a Node is cluster-scoped, and kubectl cordon takes no namespace.
	if _, err := c.Kubectl.Run([]string{c.Verb, name}); err != nil {
		// A vanished node reads as stale so the caller can relist, rather than
		// as whatever kubectl said about a name that is no longer there.
		if IsNotFound(err) {
			return "", StaleResourceError{Kind: kinds.Node, Name: name}
		}
		return "", err
	}
	return fmt.Sprintf("%s Node/%s", c.pastTense(), name), nil
}

func (c NodeCommand) pastTense() string {
	if c.Verb == "uncordon" {
		return "Uncordoned"
	}
	return "Cordoned"
}

// DrainCommand evicts the pods from an indexed node.
//
// Single-index, unlike cordon and uncordon, and unlike kx delete. Draining
// several nodes at once is a materially different operation from cordoning
// them: cordon is instant and reversible, while a drain evicts running
// workloads and blocks until they are gone, so doing it to a range of nodes in
// one command is a way to take a cluster down by typo. The help says so.
type DrainCommand struct {
	Kubectl kubectl.Service
	State   IndexResolver
	// Confirm asks for consent, returning an error to abort.
	Confirm func(string) error
}

// Execute drains one indexed node, streaming kubectl's own progress.
func (c DrainCommand) Execute(index int, yes bool, extraArgs []string) error {
	name, _, kind, err := c.State.Fields(index)
	if err != nil {
		return err
	}
	if kind != kinds.Node {
		return fmt.Errorf("drain is only supported for nodes.")
	}
	if !yes {
		if err := c.Confirm(fmt.Sprintf(
			"Evict all pods from Node/%s?", name)); err != nil {
			return err
		}
	}
	// Streamed rather than captured: a drain waits for every pod to terminate
	// and can run for minutes, and kubectl's running commentary is the only
	// sign it is doing anything.
	code, err := c.Kubectl.RunInteractive(append([]string{"drain", name}, extraArgs...), false)
	if err != nil {
		return err
	}
	if code != 0 {
		// kubectl has already said why; the code is what a script needs back.
		return SilentError{Code: code}
	}
	return nil
}

func newCordonCommand(services Services, verb string) *cobra.Command {
	var short, long string
	switch verb {
	case "cordon":
		short = "Mark one or more indexed Nodes unschedulable."
		long = "Marks a node unschedulable, so the scheduler places no new pods on it. " +
			"Pods already running there are left alone — use kx drain to evict those.\n\n" +
			"Takes several indexes, and ranges, like kx delete."
	default:
		short = "Mark one or more indexed Nodes schedulable again."
		long = "Reverses kx cordon, letting the scheduler place pods on the node again."
	}
	return &cobra.Command{
		Use:     verb + " <index>...",
		Short:   short,
		Long:    long,
		Example: "  kx " + verb + " 1\n  kx " + verb + " 1 3\n  kx " + verb + " 1..3",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes(services.State, "indexes", args)
			if err != nil {
				return err
			}
			if err := validateIndexes(services.State, indexes); err != nil {
				return err
			}
			command := NodeCommand{Kubectl: services.Kubectl, State: services.State, Verb: verb}
			// Reported one at a time, so a failure partway through leaves the
			// successes visible rather than swallowing them.
			for _, index := range indexes {
				message, err := command.Execute(index)
				if err != nil {
					return err
				}
				render.Success(message)
			}
			return nil
		},
	}
}

func newDrainCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:        "drain <index> [kubectl flags]",
		SuggestFor: []string{"evict", "evacuate"},
		Short:      "Evict the pods from an indexed Node (prompts for confirmation unless --yes).",
		Long: "Cordons a node and evicts the pods running on it, waiting for each to " +
			"terminate. Streams kubectl's own progress, which can run for minutes.\n\n" +
			"One index only, unlike kx cordon: a drain evicts running workloads and " +
			"blocks until they are gone, so applying it to a range of nodes in one " +
			"command is a way to take a cluster down by typo.\n\n" +
			"kubectl's own drain flags pass through — a drain usually needs " +
			"--ignore-daemonsets, and often --delete-emptydir-data.",
		Example: "  kx drain 1\n  kx drain 1 --ignore-daemonsets\n  kx drain 1 -y --ignore-daemonsets --delete-emptydir-data",
		// Flags are parsed by hand so kubectl's own reach it untouched. No Args
		// validator: cobra checks arity against the unstripped argv, which
		// would answer `kx drain --help` with an arity error instead of help.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			yes, rest := extractBool(rest, "--yes", "-y")
			if len(rest) == 0 {
				return fmt.Errorf("drain requires an index")
			}
			index, err := parseIndex("index", rest[0])
			if err != nil {
				return err
			}
			return DrainCommand{
				Kubectl: services.Kubectl,
				State:   services.State,
				Confirm: services.confirm(),
			}.Execute(index, yes, rest[1:])
		},
	}
	// Registered so they appear in --help; parsing is by hand.
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().Bool("ignore-daemonsets", false,
		"Continue even though DaemonSet-managed pods cannot be evicted")
	cmd.Flags().Bool("delete-emptydir-data", false,
		"Continue even though pods use emptyDir volumes, whose data is lost")
	cmd.Flags().Bool("force", false,
		"Evict pods no controller manages, which will not come back")
	cmd.Flags().Int("grace-period", -1,
		"Seconds to give each pod to terminate; -1 uses the pod's own")
	cmd.Flags().Duration("timeout", 0, "Give up waiting after this long")
	return cmd
}
