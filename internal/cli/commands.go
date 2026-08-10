package cli

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// parseIndex converts a positional argument to a 1-based index, rejecting
// anything that isn't one before a command reaches the cluster. The argument
// name is named in the error the way the Python CLI names it.
func parseIndex(name, arg string) (int, error) {
	index, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("Invalid value for '%s': '%s' is not a valid int.", name, arg)
	}
	return index, nil
}

// maxRangeSpan caps how many indexes a single range token can expand to, so a
// mistyped range like "1..999999" doesn't build a huge slice before any index
// is even resolved against the current listing.
const maxRangeSpan = 10000

// expandRange expands a "start..end" token into every index it spans,
// inclusive of both ends, walking in whichever direction start and end imply
// (so "20..9" walks down). ok is false when arg isn't a range token at all,
// so the caller falls back to treating it as a single index.
func expandRange(name, arg string) (indexes []int, ok bool, err error) {
	if !strings.Contains(arg, "..") {
		return nil, false, nil
	}
	parts := strings.Split(arg, "..")
	if len(parts) != 2 {
		return nil, true, fmt.Errorf("Invalid value for '%s': '%s' is not a valid range.", name, arg)
	}
	start, startErr := strconv.Atoi(parts[0])
	end, endErr := strconv.Atoi(parts[1])
	if startErr != nil || endErr != nil {
		return nil, true, fmt.Errorf("Invalid value for '%s': '%s' is not a valid range.", name, arg)
	}
	// start - end overflows int when the two ends sit near opposite bounds of
	// the type (e.g. "9223372036854775807..-9223372036854775808"), wrapping
	// around to a small value that would slip past the maxRangeSpan check
	// below and then loop from one end of int to the other. big.Int can't
	// overflow, so the span is computed there and converted back to int only
	// once it's already known to be small.
	span := new(big.Int).Sub(big.NewInt(int64(start)), big.NewInt(int64(end)))
	span.Abs(span)
	span.Add(span, big.NewInt(1))
	if span.Cmp(big.NewInt(maxRangeSpan)) > 0 {
		return nil, true, fmt.Errorf(
			"Invalid value for '%s': '%s' spans more than %d indexes.", name, arg, maxRangeSpan)
	}
	step := 1
	if start > end {
		step = -1
	}
	indexes = make([]int, 0, span.Int64())
	for i := start; ; i += step {
		indexes = append(indexes, i)
		if i == end {
			break
		}
	}
	return indexes, true, nil
}

func parseIndexes(name string, args []string) ([]int, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("Missing argument '%s'.", name)
	}
	indexes := make([]int, 0, len(args))
	for _, arg := range args {
		if expanded, ok, err := expandRange(name, arg); ok {
			if err != nil {
				return nil, err
			}
			indexes = append(indexes, expanded...)
			continue
		}
		index, err := parseIndex(name, arg)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}

func itemCount(count int) string {
	if count == 1 {
		return "1 item"
	}
	return strconv.Itoa(count) + " items"
}

// passthrough splits a command's arguments into the ones kx consumes and the
// ones forwarded to kubectl. See passthrough.go for why this is done by hand.
func passthrough(cmd *cobra.Command, args []string, flags func([]string) ([]string, error)) ([]string, bool, error) {
	if help, _ := extractBool(args, "-h", "--help"); help {
		return nil, true, cmd.Help()
	}
	_, rest := extractBool(args, "--no-color")
	if flags != nil {
		var err error
		rest, err = flags(rest)
		if err != nil {
			return nil, false, err
		}
	}
	return rest, false, nil
}

// splitAtDoubleDash separates kx's own arguments from a command to run inside a
// container, which is what `kx exec 1 -- ls /app` needs.
func splitAtDoubleDash(args []string) (before, after []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func newDescribeCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:                "describe <index>... [kubectl flags]",
		Short:              "Show full kubectl describe output for one or more indexed resources.",
		Example:            "  kx describe 1\n  kx describe 1 3 5\n  kx describe 1..3",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			// The leading run of numbers are the indexes; the rest belongs to
			// kubectl. The first argument is always an index, so a
			// non-numeric one is reported rather than quietly forwarded —
			// otherwise `kx describe abc` would describe nothing and succeed.
			indexArgs, extra := splitLeadingIndexes(rest)
			if len(indexArgs) == 0 && len(rest) > 0 {
				return fmt.Errorf(
					"Invalid value for 'indexes': '%s' is not a valid int.", rest[0])
			}
			indexes, err := parseIndexes("indexes", indexArgs)
			if err != nil {
				return err
			}
			command := DescribeCommand{Kubectl: services.Kubectl, State: services.State}
			for _, index := range indexes {
				name, namespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				render.Banner(string(kind), name, namespace, "")
				if err := command.Execute(index, extra); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// splitLeadingIndexes takes the run of numeric-or-range arguments at the
// front, leaving the rest for kubectl. A malformed range (e.g. "5..") still
// counts as part of the leading run — it isn't fully validated here, only
// shaped like a range, so it reaches parseIndexes for a proper error instead
// of the generic "not a valid int" that applies when nothing leads at all.
func splitLeadingIndexes(args []string) (indexes, rest []string) {
	for i, arg := range args {
		if _, err := strconv.Atoi(arg); err != nil && !strings.Contains(arg, "..") {
			return args[:i], args[i:]
		}
	}
	return args, nil
}

func newLogsCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:   "logs <index>... [kubectl flags]",
		Short: "Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services.",
		Long: "Streams logs for an indexed resource. Deployments, StatefulSets,\n" +
			"DaemonSets and Services aggregate logs across the pods they own.",
		Example:            "  kx logs 1\n  kx logs 1 2\n  kx logs 1 -f --tail=100\n  kx logs 1..3",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			// Indexes lead, kubectl's flags follow — the same split describe
			// uses. Without it a second index reaches kubectl as a positional,
			// where it means a container name.
			indexArgs, extra := splitLeadingIndexes(rest)
			if len(indexArgs) == 0 {
				if len(rest) > 0 {
					return fmt.Errorf(
						"Invalid value for 'indexes': '%s' is not a valid int.", rest[0])
				}
				return fmt.Errorf("Missing argument 'indexes'.")
			}
			indexes, err := parseIndexes("indexes", indexArgs)
			if err != nil {
				return err
			}
			if err := checkFollow(extra, len(indexes)); err != nil {
				return err
			}

			command := LogsCommand{
				Kubectl: services.Kubectl, State: services.State, Status: render.Status,
			}
			for position, index := range indexes {
				name, namespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				if position > 0 {
					render.Blank()
				}
				render.Banner(string(kind), name, namespace, "")
				if err := command.Execute(index, extra); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// checkFollow refuses to follow several pods at once.
//
// Streaming them in turn would block on the first and silently never reach the
// rest. Aggregating a workload's pods — `kx logs <deployment index>` — is the
// supported way to follow more than one.
func checkFollow(args []string, indexes int) error {
	follow, _ := extractBool(args, "-f", "--follow")
	if follow && indexes > 1 {
		return fmt.Errorf(
			"--follow streams a single pod; give one index, or use the workload's index to aggregate")
	}
	return nil
}

func newEditCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:                "edit <index> [kubectl flags]",
		Short:              "Open an indexed resource in your editor via kubectl edit.",
		Example:            "  kx edit 2",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			// Cobra's arity check ran against the unstripped argv, so an
			// argument list of nothing but kx's own flags reaches here empty.
			if len(rest) == 0 {
				return fmt.Errorf("edit requires an index")
			}
			index, err := parseIndex("index", rest[0])
			if err != nil {
				return err
			}
			return EditCommand{Kubectl: services.Kubectl, State: services.State}.
				Execute(index, rest[1:])
		},
	}
}

func newExecCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <index> [kubectl flags] [-- command...]",
		Short: "Open an interactive shell in an indexed pod (bash, falling back to sh).",
		Long: "Runs a command inside an indexed pod. With no command, tries each\n" +
			"configured shell in turn (bash, then sh by default).",
		Example:            "  kx exec 1\n  kx exec 1 -- ls /app\n  kx exec 1 -c sidecar",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			before, command := splitAtDoubleDash(args)
			rest, handled, err := passthrough(cmd, before, nil)
			if err != nil || handled {
				return err
			}
			if len(rest) == 0 {
				return fmt.Errorf("exec requires an index")
			}
			index, err := parseIndex("index", rest[0])
			if err != nil {
				return err
			}
			return ExecCommand{
				Kubectl: services.Kubectl, State: services.State, Shells: services.Config.Shells,
			}.Execute(index, command, rest[1:])
		},
	}
}

func newDeleteCommand(services Services) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <index>...",
		Short:   "Delete one or more indexed resources (prompts for confirmation unless --yes).",
		Example: "  kx delete 3\n  kx delete 3 5 -y\n  kx delete 3..5",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes("indexes", args)
			if err != nil {
				return err
			}
			command := DeleteCommand{
				Kubectl: services.Kubectl,
				State:   services.State,
				Confirm: render.Confirm,
				Status:  render.Status,
			}
			// Confirmed and reported one at a time, so declining one resource
			// doesn't silently take the rest with it.
			for _, index := range indexes {
				message, err := command.Execute(index, yes)
				if err != nil {
					return err
				}
				render.Success(message)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

func newScaleCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:     "scale <index> <replicas>",
		Short:   "Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count.",
		Example: "  kx scale 1 3",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex("index", args[0])
			if err != nil {
				return err
			}
			replicas, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("'%s' is not a replica count", args[1])
			}
			message, err := ScaleCommand{Kubectl: services.Kubectl, State: services.State}.
				Execute(index, replicas)
			if err != nil {
				return err
			}
			render.Success(message)
			return nil
		},
	}
}

func newRolloutCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:       "rollout <action> <index>",
		Short:     "Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet.",
		Example:   "  kx rollout status 1\n  kx rollout restart 1\n  kx rollout undo 1",
		ValidArgs: []string{"status", "restart", "pause", "resume", "history", "undo"},
		Args:      cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex("index", args[1])
			if err != nil {
				return err
			}
			output, err := RolloutCommand{Kubectl: services.Kubectl, State: services.State}.
				Execute(args[0], index)
			if err != nil {
				return err
			}
			if strings.TrimSpace(output) != "" {
				// Printed with its trailing newline intact, so consecutive
				// manifests are separated the way kubectl's own output is.
				render.Raw(strings.TrimRight(output, "\n") + "\n")
			}
			return nil
		},
	}
}

func newPortForwardCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:     "port-forward <index> <port> [kubectl flags]",
		Short:   "Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service).",
		Example: "  kx port-forward 1 8080:80",
		// No Args validator: cobra's arity check runs against the
		// unstripped argv, before passthrough can pull --help out of it —
		// `kx port-forward --help` is a single argument, which used to fail
		// a MinimumNArgs(2) gate before RunE ever saw it. The real "need an
		// index and a port" check happens below, once passthrough has
		// already resolved --help.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			if len(rest) < 2 {
				return fmt.Errorf("port-forward requires an index and a port")
			}
			index, err := parseIndex("index", rest[0])
			if err != nil {
				return err
			}
			return PortForwardCommand{Kubectl: services.Kubectl, State: services.State}.
				Execute(index, rest[1], rest[2:])
		},
	}
}

func newCopyCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cp <src> <dest> [kubectl flags]",
		Short: "Copy files to or from an indexed pod via kubectl cp.",
		Example: "  kx cp 1:/var/log/app.log ./app.log\n" +
			"  kx cp ./patch.conf 1:/etc/app/patch.conf",
		// No Args validator: cobra's arity check runs against the
		// unstripped argv, before passthrough can pull --help out of it —
		// `kx cp --help` is a single argument, which would fail a
		// MinimumNArgs(2) gate before RunE ever saw it. The real "need a
		// source and a destination" check happens below, once passthrough
		// has already resolved --help.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			if len(rest) < 2 {
				return fmt.Errorf("cp requires a source and a destination")
			}
			return CopyCommand{Kubectl: services.Kubectl, State: services.State}.
				Execute(rest[0], rest[1], rest[2:])
		},
	}
	// Pure kubectl passthrough, parsed by hand — registered only so they
	// appear in --help instead of vanishing.
	cmd.Flags().StringP("container", "c", "", "Container name, if the pod has more than one")
	cmd.Flags().Bool("no-preserve", false,
		"Don't preserve the copied file/directory's ownership and permissions")
	cmd.Flags().Int("retries", 0, "Number of retries on a copy failure (0 disables)")
	return cmd
}

func newYamlCommand(services Services) *cobra.Command {
	var show string
	cmd := &cobra.Command{
		Use:     "yaml <index>...",
		Short:   "Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields.",
		Example: "  kx yaml 1\n  kx yaml 1 2\n  kx yaml 1 --show metadata,spec\n  kx yaml 1..3",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes("indexes", args)
			if err != nil {
				return err
			}
			var fields []string
			if show != "" {
				for _, field := range strings.Split(show, ",") {
					if trimmed := strings.TrimSpace(field); trimmed != "" {
						fields = append(fields, trimmed)
					}
				}
			}
			command := YamlCommand{Kubectl: services.Kubectl, State: services.State}
			for position, index := range indexes {
				name, namespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				if position > 0 {
					render.Raw("")
				}
				// Banner per manifest: without it, several manifests run
				// together with nothing saying which is which.
				render.Banner(string(kind), name, namespace, "")
				stop := render.Status("fetching manifest")
				output, err := command.Execute(index, fields)
				stop()
				if err != nil {
					return err
				}
				// Printed with its trailing newline intact, so consecutive
				// manifests are separated the way kubectl's own output is.
				render.Raw(strings.TrimRight(output, "\n") + "\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&show, "show", "", "Comma-separated fields to display (e.g. metadata,spec)")
	return cmd
}

func newMetadataReadCommand(services Services, use, short, field, header string, selector bool) *cobra.Command {
	var asSelector bool
	cmd := &cobra.Command{
		Use:     use + " <index>...",
		Short:   short,
		Args:    cobra.MinimumNArgs(1),
		Example: "  kx " + use + " 1\n  kx " + use + " 1 2 3\n  kx " + use + " 1..3",
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes, err := parseIndexes("indexes", args)
			if err != nil {
				return err
			}
			command := MetadataReadCommand{
				Kubectl: services.Kubectl, State: services.State, Field: field,
			}
			for position, index := range indexes {
				stop := render.Status("fetching " + field)
				keys, values, err := command.Execute(index)
				stop()
				if err != nil {
					return err
				}
				name, namespace, kind, err := services.State.Fields(index)
				if err != nil {
					return err
				}
				if position > 0 {
					render.Blank()
				}
				render.Banner(string(kind), name, namespace, itemCount(len(keys)))
				if asSelector {
					pairs := make([]string, 0, len(keys))
					for _, key := range keys {
						pairs = append(pairs, key+"="+values[key])
					}
					render.Raw(strings.Join(pairs, ","))
					continue
				}
				render.KeyValueTable(header, keys, values)
			}
			return nil
		},
	}
	if selector {
		cmd.Flags().BoolVarP(&asSelector, "selector", "s", false,
			"Output as a copy-pastable label selector")
	}
	return cmd
}

func newMetadataWriteCommand(services Services, verb, field, short string) *cobra.Command {
	var (
		removes   []string
		overwrite bool
	)
	cmd := &cobra.Command{
		Use:     verb + " <index> [key=value...]",
		Short:   short,
		Args:    cobra.MinimumNArgs(1),
		Example: "  kx " + verb + " 1 env=prod\n  kx " + verb + " 1 --remove env",
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := parseIndex("index", args[0])
			if err != nil {
				return err
			}
			keys, values, err := parsePairs(args[1:])
			if err != nil {
				return err
			}
			message, err := MetadataWriteCommand{
				Kubectl: services.Kubectl, State: services.State, Verb: verb, Field: field,
			}.Execute(index, keys, values, removes, overwrite)
			if err != nil {
				return err
			}
			render.Success(message)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&removes, "remove", nil, "Key to remove (repeatable)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Allow replacing an existing key")
	return cmd
}

// newSwitchCommand builds the namespace and context commands, which share a
// shape: no argument lists, an index switches.
func newSwitchCommand(services Services, use, alias, short string, isContext bool) *cobra.Command {
	return &cobra.Command{
		Use:     use + " [index]",
		Short:   short,
		Aliases: []string{alias},
		Args:    cobra.MaximumNArgs(1),
		Example: "  kx " + use + "\n  kx " + use + " 2",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listSwitchTargets(services, isContext)
			}
			index, err := parseIndex("index", args[0])
			if err != nil {
				return err
			}
			// Shared with `kx get contexts <index>`, which routes here too, so
			// the stale-namespace relist lives in one place.
			return switchTo(services, use, index, isContext)
		},
	}
}

func listSwitchTargets(services Services, isContext bool) error {
	if isContext {
		stop := render.Status("fetching contexts")
		// The caption comes back with the listing rather than out of state: the
		// listing no longer goes into history, so there is nothing there to read
		// it from — on a fresh install nothing at all, and otherwise whatever
		// resource listing happened to be current.
		output, current, err := ContextsCommand{
			Kubectl: services.Kubectl, State: services.State, Index: services.Index,
		}.Execute()
		stop()
		if err != nil {
			return err
		}
		render.IndexedTable(output, "Contexts", current, "")
		return nil
	}

	stop := render.Status("fetching namespaces")
	// Slot only: `kx ns` is a switch listing, not work. `kx get ns` remains the
	// way to put namespaces in history for `kx describe <n>` and friends.
	output, namespace, err := GetCommand{
		Kubectl: services.Kubectl,
		State:   slotOnly{writer: services.State},
		Index:   services.Index,
	}.Execute("namespaces", "", nil)
	stop()
	if err != nil {
		return err
	}
	render.IndexedTable(output, "namespaces", namespace, "")
	return nil
}

func newStateCommand(services Services) *cobra.Command {
	var all, targets bool
	cmd := &cobra.Command{
		Use:   "state [position]",
		Short: "Show current state, jump to a history position, list all entries with --all, or expand the switch targets with --targets.",
		Example: "  kx state\n  kx state --all\n  kx state --targets\n" +
			"  kx state 2",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Both read the whole file, and the slots live outside the stack, so
			// --targets works on a history that is empty — the shape a fresh
			// install has after `kx ns`.
			if all || targets {
				history, err := services.State.LoadHistory()
				// No state file yet is not a failure for either view — it is
				// the shape a new install has. Each renderer says what fills
				// the thing it shows; ErrNoState names `kx get`, which is only
				// half the answer and the wrong half for --targets.
				if errors.Is(err, state.ErrNoState) {
					history, err = state.History{}, nil
				}
				if err != nil {
					return err
				}
				if targets {
					render.SwitchTargets(history)
					return nil
				}
				render.StateHistory(history)
				return nil
			}
			if len(args) == 1 {
				position, err := parseIndex("position", args[0])
				if err != nil {
					return err
				}
				entry, err := services.State.NavigateTo(position)
				if err != nil {
					return err
				}
				render.State(entry)
				return nil
			}
			entry, err := services.State.Load()
			if err != nil {
				return err
			}
			render.State(entry)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show the full history stack")
	cmd.Flags().BoolVarP(&targets, "targets", "t", false,
		"Show the namespace and context listings the switch commands index into")
	return cmd
}

func newNavigateCommand(services Services, use, short string, delta int) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := services.State.Navigate(delta)
			if err != nil {
				return err
			}
			render.State(entry)
			return nil
		},
	}
}

func newDropCommand(services Services) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "drop <position>",
		Short:   "Remove a history entry by position (shown in kx state --all); --all clears everything, including namespace/context slots.",
		Example: "  kx state drop 2\n  kx state drop --all",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 {
					return fmt.Errorf("drop --all takes no position argument")
				}
				if err := services.confirm()(
					"Clear all kx history, including namespace and context slots?",
				); err != nil {
					return err
				}
				if err := services.State.DropAll(); err != nil {
					return err
				}
				render.Success("Cleared all history.")
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("drop requires a position, or --all to clear everything")
			}
			position, err := parseIndex("position", args[0])
			if err != nil {
				return err
			}
			history, err := services.State.Drop(position)
			if err != nil {
				return err
			}
			render.StateHistory(history)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Clear all history and namespace/context slots")
	return cmd
}
