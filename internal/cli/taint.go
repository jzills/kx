package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/spf13/cobra"
)

// taintEffects are the effects a taint can have, in the order kubectl lists
// them.
var taintEffects = []string{"NoSchedule", "PreferNoSchedule", "NoExecute"}

// TaintCommand sets or removes taints on an indexed Node.
//
// One index, as kx label takes one, and for drain's reason as well: a
// NoExecute taint evicts every Pod that does not tolerate it, so a range of
// Nodes tainted in one command is a drain of each of them.
type TaintCommand struct {
	Kubectl kubectl.Service
}

// Execute applies the taints in sets and drops those in removes, returning
// kx's line for it and kubectl's own output, which reportChange prints in its
// place when an output format was asked for. Both are already validated
// (parseTaint, parseTaintRemoval). extraArgs are kubectl's own flags,
// forwarded after kx's. target is resolved once, by the caller, so the Node
// its checks read is the Node tainted.
func (c TaintCommand) Execute(target Resolved, sets, removes, extraArgs []string) (message, output string, err error) {
	// The kind first: an index that is no Node cannot be tainted whatever
	// is asked of it, and asking for the taints first sent the user to write
	// one before learning that.
	if target.Kind != kinds.Node {
		return "", "", unsupportedKindError("taint", target.Kind, kinds.Set{kinds.Node})
	}
	if len(sets) == 0 && len(removes) == 0 {
		return "", "", errors.New(
			"kx taint needs key=value:Effect to set, or --remove to name a taint to drop — neither was given.")
	}
	name := target.Name
	// No -n: a Node is cluster-scoped.
	args := append([]string{"taint", "node", name}, sets...)
	for _, taint := range removes {
		args = append(args, taint+"-")
	}
	output, err = c.Kubectl.Run(append(args, extraArgs...))
	if err != nil {
		// Probed rather than read off the error: kubectl answers a --remove
		// of a taint the Node does not have with `taint "gpu" not found`,
		// which IsNotFound would take for the Node itself having gone.
		if c.Kubectl.Probe([]string{"get", "node", name}) != 0 {
			return "", "", StaleResourceError{Kind: kinds.Node, Name: name, Ref: target.Ref}
		}
		// kubectl's words, but no longer a kubectl.Error: withRefresh asks
		// IsNotFound again (isStale), and would relist the Nodes over it.
		return "", "", errors.New(err.Error())
	}
	var parts []string
	if len(sets) > 0 {
		parts = append(parts, fmt.Sprintf("set %d", len(sets)))
	}
	if len(removes) > 0 {
		parts = append(parts, fmt.Sprintf("removed %d", len(removes)))
	}
	// kx replaces kubectl's output with this line, so it has to say when
	// nothing was changed — as kx label's does.
	if isDryRun(extraArgs) {
		parts = append(parts, "dry run — nothing was changed")
	}
	return fmt.Sprintf("Tainted Node/%s (%s)", name, strings.Join(parts, ", ")), output, nil
}

// parseTaint checks one taint to set, key=value:Effect or key:Effect, before
// kubectl sees it, so a typo is refused in kx's words rather than kubectl's.
func parseTaint(taint string) error {
	if strings.HasSuffix(taint, "-") {
		return fmt.Errorf(
			"'%s' is kubectl's spelling for removing a taint — use '--remove %s' instead.",
			taint, strings.TrimSuffix(taint, "-"))
	}
	spec, effect, found := strings.Cut(taint, ":")
	key, _, _ := strings.Cut(spec, "=")
	if !found || key == "" {
		return fmt.Errorf(
			"'%s' is not a taint — write key=value:Effect or key:Effect, as in 'dedicated=gpu:NoSchedule'.", taint)
	}
	return validTaintEffect(taint, effect)
}

// parseTaintRemoval checks one --remove value: a key, which drops the key's
// taints of every effect, or key:Effect, which drops the one.
func parseTaintRemoval(taint string) error {
	key, effect, found := strings.Cut(taint, ":")
	if key == "" || strings.Contains(key, "=") {
		return fmt.Errorf(
			"Invalid value for '--remove': '%s' is not a valid taint key — give the key, or key:Effect.", taint)
	}
	if !found {
		return nil
	}
	return validTaintEffect(taint, effect)
}

func validTaintEffect(taint, effect string) error {
	if !slices.Contains(taintEffects, effect) {
		return fmt.Errorf("Invalid value for the effect in '%s': '%s'. Accepted values: %s.",
			taint, effect, strings.Join(taintEffects, ", "))
	}
	return nil
}

// refuseTaintScopeFlag refuses a flag that would widen a taint from the
// indexed Node to others, read from everything kx forwards, the part after
// '--' included. Beside a Node's name kubectl taint does not refuse them, as
// kubectl label does: --all taints every Node in the cluster, whatever the
// name says. --all=false is the flag's own default and selects nothing, so
// it is read as a boolean, as kubectl reads it, rather than refused on sight.
func refuseTaintScopeFlag(extra []string) error {
	spelling := ""
	if all, _ := extractBool(extra, "--all"); all {
		spelling = "--all"
	} else if hasFlag(extra, "--selector", "-l") {
		spelling = firstSpelling(extra, "--selector", "-l")
	} else {
		return nil
	}
	return fmt.Errorf(
		"'%s' cannot be combined with an index — kubectl taint would apply it to every Node "+
			"it selects, not only the indexed one.", spelling)
}

func newTaintCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "taint <index> [taint...] [kubectl flags]",
		Short: "Set or remove taints on an indexed Node.",
		Long: "Sets or removes taints on one indexed Node — key=value:Effect or key:Effect to set, " +
			"--remove to drop one. The effect is NoSchedule, PreferNoSchedule or NoExecute.\n\n" +
			"One index only, as for kx drain: a NoExecute taint evicts every Pod that does not " +
			"tolerate it, so tainting a range of Nodes in one command drains each of them.\n\n" +
			"kubectl's own taint flags pass through, --overwrite and --dry-run among them. " +
			"--all and --selector are refused, since beside a Node's name kubectl taint applies " +
			"them to every Node they select.",
		Example: "  kx taint 1 dedicated=gpu:NoSchedule\n" +
			"  kx taint 1 --remove dedicated\n" +
			"  kx taint 1 dedicated=infra:NoSchedule --overwrite\n" +
			"  kx taint 1 maintenance:NoExecute --dry-run=server",
		Annotations: mutatingAnnotations,
	}
	cmd.Flags().StringArray("remove", nil, "Taint to remove, as key or key:Effect (repeatable)")
	registerChangeFlags(cmd)
	kubectlFlags := registerKubectlFlags(cmd, "taint")
	// Still parsed, so refuseTaintScopeFlag can name them, but not offered:
	// kx taint refuses both beside its index.
	for _, name := range []string{"all", "selector"} {
		_ = cmd.Flags().MarkHidden(name)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		positional, extra := kubectlArgv(cmd, args, kubectlFlags)
		if len(positional) == 0 {
			return requiredArgsError(cmd)
		}
		installAgentIndexNotice(services)
		ref, err := parseRef("index", positional[0])
		if err != nil {
			return err
		}
		sets := positional[1:]
		for _, taint := range sets {
			if err := parseTaint(taint); err != nil {
				return err
			}
		}
		removes, _ := cmd.Flags().GetStringArray("remove")
		for _, taint := range removes {
			if err := parseTaintRemoval(taint); err != nil {
				return err
			}
		}
		if err := refuseTaintScopeFlag(extra); err != nil {
			return err
		}
		name, namespace, kind, err := services.State.Resolve(ref)
		if err != nil {
			return err
		}
		target := Resolved{Ref: ref, Name: name, Namespace: namespace, Kind: kind}
		if err := refuseScopeFlagResolved([]Resolved{target}, extra); err != nil {
			return err
		}
		message, output, err := TaintCommand{Kubectl: services.Kubectl}.
			Execute(target, sets, removes, extra)
		if err != nil {
			return err
		}
		reportChange(extra, output, message)
		return nil
	}
	return cmd
}
