package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// The helpers below read flags out of an argv by hand.
//
// Every command that forwards to kubectl now has cobra parse its argv against
// kubectl's own flag table (kubectlargs.go) and forwards each flag as
// --long=value. kx's code that reads one of those flags back — the namespace
// for a caption, --since to rewrite it, --dry-run for kx's own line — still
// reads it from that forwarded argv with these helpers, which accept every
// spelling kubectl does.
//
// kx scan is the one command that still splits its argv by hand: it forwards
// to the vulnerability scanner, whose flags kx has no table for. It sets
// DisableFlagParsing, and these helpers remove kx's own flags from the argv
// and leave the scanner's untouched.

// passthrough answers -h/--help for a DisableFlagParsing command and drops
// --no-color, which main has already applied from the raw arguments, returning
// the rest to forward.
func passthrough(cmd *cobra.Command, args []string) ([]string, bool, error) {
	if help, _ := extractBool(args, "-h", "--help"); help {
		return nil, true, cmd.Help()
	}
	_, rest := extractBool(args, "--no-color")
	return rest, false, nil
}

// extractString removes a string flag and its value from args, accepting
// "--long value", "--long=value", "-s value", "-s=value" and the attached
// shorthand "-svalue" (the spelling kubectl users type constantly, e.g.
// "-nprod"). A flag given more than once keeps the last value, matching
// pflag.
func extractString(args []string, long, short string) (value string, rest []string, err error) {
	values, rest, err := extractStrings(args, long, short)
	if len(values) > 0 {
		value = values[len(values)-1]
	}
	return value, rest, err
}

// extractStrings is extractString for a repeatable flag: it removes every
// occurrence and returns each value in the order given.
func extractStrings(args []string, long, short string) (values, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == long || (short != "" && arg == short):
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("'%s' needs a value.", arg)
			}
			values = append(values, args[i+1])
			i++
		case strings.HasPrefix(arg, long+"="):
			values = append(values, strings.TrimPrefix(arg, long+"="))
		case short != "" && strings.HasPrefix(arg, short+"="):
			values = append(values, strings.TrimPrefix(arg, short+"="))
		// Attached shorthand, e.g. "-nprod". Checked last and guarded on
		// length so a bare "-n" still falls into the exact-match case above
		// and takes the following argument instead of being trimmed to "".
		case short != "" && len(arg) > len(short) && strings.HasPrefix(arg, short):
			values = append(values, strings.TrimPrefix(arg, short))
		default:
			rest = append(rest, arg)
		}
	}
	return values, rest, nil
}

// hasFlag reports whether a flag appears in args, in any spelling extractString
// accepts.
//
// Presence is not the same question as value. `-k ""` names a key the user
// asked for and a whole-Secret dump they did not, so an explicitly empty value
// has to be distinguishable from an absent flag — which the returned value
// alone cannot do.
//
// This must recognize exactly the spellings extractString consumes,
// attached shorthand included — a caller that checks presence before
// extracting the value (like scan's --namespace/--all-namespaces guard)
// would otherwise miss a spelling extractString quietly consumes anyway.
func hasFlag(args []string, long, short string) bool {
	for _, arg := range args {
		switch {
		case arg == long, short != "" && arg == short:
			return true
		case strings.HasPrefix(arg, long+"="):
			return true
		case short != "" && strings.HasPrefix(arg, short+"="):
			return true
		case short != "" && len(arg) > len(short) && strings.HasPrefix(arg, short):
			return true
		}
	}
	return false
}

// extractBool removes a boolean flag from args and reports whether it was
// present. Each name also accepts "<name>=<value>" (e.g. "-A=true"), with the
// value parsed by strconv.ParseBool: "=false" means the flag counts as
// absent, and a value that fails to parse (e.g. "=banana") counts as present,
// since the user plainly meant to pass the flag. Attached shorthand
// ("-Atrue") is deliberately not supported — booleans don't take attached
// values, so that spelling isn't real.
func extractBool(args []string, names ...string) (present bool, rest []string) {
	rest = make([]string, 0, len(args))
	for _, arg := range args {
		matched := false
		for _, name := range names {
			if arg == name {
				matched = true
				present = true
				break
			}
			if strings.HasPrefix(arg, name+"=") {
				matched = true
				if parsed, err := strconv.ParseBool(strings.TrimPrefix(arg, name+"=")); err == nil {
					present = parsed
				} else {
					present = true
				}
				break
			}
		}
		if matched {
			continue
		}
		rest = append(rest, arg)
	}
	return present, rest
}

// sweepInsteadHint is what the commands that can sweep a namespace offer as
// the alternative to dropping the flag. The commands that only act on an index
// have no such alternative, and say nothing rather than inventing one.
const sweepInsteadHint = "Drop the flag, or drop the index to sweep the namespace instead."

// scopeFlagBesideIndexError reports a namespace-scope flag given alongside an
// index, quoting the spelling that was typed.
//
// One sentence in one place: the rule is enforced by every command that
// resolves an index, and a reader moving between them should meet the same
// explanation rather than working out whether two wordings mean the same
// thing. hint is appended when the caller has something to offer instead.
func scopeFlagBesideIndexError(flag, hint string) error {
	message := fmt.Sprintf(
		"'%s' cannot be combined with an index — an index already carries the "+
			"namespace it was listed from.", flag)
	if hint != "" {
		message += " " + hint
	}
	return errors.New(message)
}

// clusterFlags are kubectl's flags that choose the cluster a command reaches,
// as long and short spellings. --user, --token and --as are not among them:
// they change who acts, not where, and an index says nothing about that.
var clusterFlags = [][2]string{
	{"--context", ""}, {"--kubeconfig", ""}, {"--cluster", ""}, {"--server", "-s"},
}

// clusterFlagIn reports the cluster-selecting flag present in argv, spelled the
// way it was typed, or "" for none.
func clusterFlagIn(args []string) string {
	for _, flag := range clusterFlags {
		long, short := flag[0], flag[1]
		if !hasFlag(args, long, short) {
			continue
		}
		if short == "" {
			return long
		}
		return firstSpelling(args, long, short)
	}
	return ""
}

// clusterFlagBesideIndexError reports a cluster-selecting flag given beside an
// index. An index is counted in one cluster, and kx refuses to spend it in
// another (state's ContextMismatchError); the flag would have spent it there
// anyway, on whatever the other cluster has under the same name.
func clusterFlagBesideIndexError(flag string) error {
	return fmt.Errorf(
		"'%s' cannot be combined with an index — an index already carries the "+
			"cluster it was listed from. To act on another cluster, switch to it "+
			"with 'kx context' and list it there.", flag)
}

// crossClusterCaption heads a listing taken with a cluster-selecting flag,
// which kx prints but never numbers: an index is spent in the cluster it was
// counted in, and this listing's cluster is not the one kx resolves against.
// Worded as kx get --watch's caption is, for the same kind of output.
func crossClusterCaption(flag string) string {
	return "listings with '" + flag + "' can't be indexed — printing kubectl output directly"
}

// refuseScopeFlag rejects a namespace-scope flag in args that would contradict
// a resolved index, given the namespace that index resolved to. A
// cluster-selecting flag is refused beside any index, cluster-scoped or not.
//
// kubectl takes the last -n it is given, and kx appends its own from the index
// — so a second one silently won. For `kx delete` that meant a confirmation
// prompt naming the namespace the index came from and a deletion somewhere
// else entirely.
//
// A cluster-scoped index is the exception, and not merely a harmless one:
// state records no namespace for a Node, so there is nothing for -n to
// contradict, and `kx debug <node-index>` creates a pod whose namespace is
// exactly what -n chooses (see DebugCommand.Execute). -A is refused either
// way — there is no listing here for it to widen.
func refuseScopeFlag(args []string, namespace string) error {
	if flag := clusterFlagIn(args); flag != "" {
		return clusterFlagBesideIndexError(flag)
	}
	flag := scopeFlagIn(args)
	switch flag {
	case "":
		return nil
	case "--namespace", "-n":
		if namespace == "" {
			return nil
		}
	}
	return scopeFlagBesideIndexError(flag, "")
}

// refuseScopeFlagResolved rejects a namespace-scope flag that would contradict
// a reference the caller has already resolved.
//
// Takes the resolved values rather than a resolver and a list of indexes: the
// namespace it needs is on each Resolved, and asking for it again cost a state
// load per index on a path that had the answer in hand.
func refuseScopeFlagResolved(resolved []Resolved, args []string) error {
	if flag := clusterFlagIn(args); flag != "" {
		return clusterFlagBesideIndexError(flag)
	}
	if scopeFlagIn(args) == "" {
		return nil
	}
	if len(resolved) == 0 {
		return refuseScopeFlag(args, "")
	}
	for _, target := range resolved {
		if err := refuseScopeFlag(args, target.Namespace); err != nil {
			return err
		}
	}
	return nil
}
