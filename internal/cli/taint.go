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
	// Confirm asks for consent before a NoExecute taint, returning an error
	// to abort, as kx drain's does.
	Confirm func(string) error
}

// Execute applies the taints in sets and drops those in removes, returning
// kx's line for it and kubectl's own output, which reportChange prints in its
// place when an output format was asked for. Both are already validated
// (parseTaint, parseTaintRemoval). extraArgs are kubectl's own flags,
// forwarded after kx's. target is resolved once, by the caller, so the Node
// its checks read is the Node tainted, and source is that same resolve's
// provenance, named in the prompt.
func (c TaintCommand) Execute(
	target Resolved, sets, removes, extraArgs []string, yes bool, source string,
) (message, output string, err error) {
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
	// A NoExecute taint evicts every Pod that does not tolerate it, which is
	// a drain by another name, so it is asked about the way kx drain asks.
	// The other two effects evict nothing and are set without a word.
	evicting := evictingTaints(sets)
	if taintPrompts(evicting, yes, extraArgs) {
		if err := c.Confirm(fmt.Sprintf(
			"Evict every Pod on Node/%s that does not tolerate '%s'%s?",
			name, strings.Join(evicting, "', '"), listingProvenance(source))); err != nil {
			return "", "", err
		}
	}
	// No -n: a Node is cluster-scoped.
	args := append([]string{"taint", "node", name}, sets...)
	for _, taint := range removes {
		args = append(args, taint+"-")
	}
	output, err = c.Kubectl.Run(append(args, extraArgs...))
	if err != nil {
		// Asked again rather than read off the error: kubectl answers a
		// --remove of a taint the Node does not have with `taint "gpu" not
		// found`, which IsNotFound would take for the Node itself having
		// gone.
		//
		// Only a NotFound from that second look is a vanished Node, as for
		// kx drain's preflight. Any nonzero exit counted, so an API server
		// that could not be reached — which fails the taint and the look
		// alike — was reported as a Node that no longer exists, and the
		// refresh then relisted over kubectl's real complaint.
		if _, missing := c.Kubectl.Run([]string{"get", "node", name}); IsNotFound(missing) {
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

// taintPrompts reports whether Execute will ask before setting evicting, the
// NoExecute taints among those given: waved through by --yes, and by a dry
// run, which changes nothing to ask about.
//
// Read by RunE as well, to arm the agent-listing notice only where no prompt
// will name the provenance itself — the two must agree, or a taint that
// prompts says where the index came from twice, and one that does not says
// it never.
func taintPrompts(evicting []string, yes bool, extraArgs []string) bool {
	return len(evicting) > 0 && !yes && !isDryRun(extraArgs)
}

// evictingTaints are the taints among sets whose effect is NoExecute, in the
// order given. Read off the taints kx has already validated (parseTaint), so
// the effect is whatever follows the last colon.
func evictingTaints(sets []string) []string {
	var evicting []string
	for _, taint := range sets {
		if _, effect, found := strings.Cut(taint, ":"); found && effect == "NoExecute" {
			evicting = append(evicting, taint)
		}
	}
	return evicting
}

// parseTaint checks one taint to set, key=value:Effect or key:Effect, before
// kubectl sees it, so a typo is refused in kx's words rather than kubectl's.
func parseTaint(taint string) error {
	if strings.HasSuffix(taint, "-") {
		return fmt.Errorf(
			"'%s' is kubectl's spelling for removing a taint — use '--remove %s' instead.",
			taint, removalSpelling(strings.TrimSuffix(taint, "-")))
	}
	spec, effect, found := strings.Cut(taint, ":")
	key, _, _ := strings.Cut(spec, "=")
	if !found || key == "" {
		return fmt.Errorf(
			"'%s' is not a taint — write key=value:Effect or key:Effect, as in 'dedicated=gpu:NoSchedule'.", taint)
	}
	return validTaintEffect(taint, effect)
}

// removalSpelling is the --remove value for a taint written out in full:
// its key, and its effect where one was given. The value is dropped, because
// a taint is removed by key and effect and never by value — kubectl's own
// key=value:Effect- removes whatever value the key holds — and suggesting it
// sent the reader from one refusal into the next, --remove having answered
// 'dedicated=gpu:NoSchedule' with "not a valid taint key".
func removalSpelling(taint string) string {
	spec, effect, found := strings.Cut(taint, ":")
	key, _, _ := strings.Cut(spec, "=")
	if !found {
		return key
	}
	return key + ":" + effect
}

// parseTaintRemoval checks one --remove value: a key, which drops the key's
// taints of every effect, or key:Effect, which drops the one.
func parseTaintRemoval(taint string) error {
	key, effect, found := strings.Cut(taint, ":")
	// The trailing '-' kubectl removes a taint by. Execute appends one of
	// its own, so kubectl saw 'dedicated--' and answered with the regex a
	// taint key has to match; the set path already names this mistake.
	if strings.HasSuffix(key, "-") {
		return fmt.Errorf(
			"Invalid value for '--remove': '%s' ends in kubectl's removal '-' — "+
				"'--remove' adds it, so give the key as '%s'.", taint, strings.TrimSuffix(key, "-"))
	}
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
			"tolerate it, so tainting a range of Nodes in one command drains each of them. " +
			"Setting one asks first, unless you pass --yes.\n\n" +
			"kubectl's own taint flags pass through, --overwrite and --dry-run among them. " +
			"--all and --selector are refused, since beside a Node's name kubectl taint applies " +
			"them to every Node they select.",
		Example: "  kx taint 1 dedicated=gpu:NoSchedule\n" +
			"  kx taint 1 --remove dedicated\n" +
			"  kx taint 1 maintenance:NoExecute -y\n" +
			"  kx taint 1 dedicated=infra:NoSchedule --overwrite\n" +
			"  kx taint 1 maintenance:NoExecute --dry-run=server",
		Annotations: mutatingAnnotations,
	}
	cmd.Flags().StringArray("remove", nil, "Taint to remove, as key or key:Effect (repeatable)")
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt a NoExecute taint asks")
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
		yes, _ := cmd.Flags().GetBool("yes")
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
		// Armed only where no prompt will name the listing's provenance
		// itself, as kx drain arms it only with --yes. Unlike drain, kx
		// taint does not always prompt: a NoSchedule taint evicts nothing
		// and is set without a question, so the notice is what says the
		// index came from an agent. Before the resolve below, which is what
		// it hooks.
		if !taintPrompts(evictingTaints(sets), yes, extra) {
			installAgentIndexNotice(services)
		}
		name, namespace, kind, source, err := resolveWithProvenance(services.State, ref)
		if err != nil {
			return err
		}
		target := Resolved{Ref: ref, Name: name, Namespace: namespace, Kind: kind}
		if err := refuseScopeFlagResolved([]Resolved{target}, extra); err != nil {
			return err
		}
		message, output, err := TaintCommand{Kubectl: services.Kubectl, Confirm: services.confirm()}.
			Execute(target, sets, removes, extra, yes, source)
		if err != nil {
			return err
		}
		reportChange(extra, output, message)
		return nil
	}
	return cmd
}
