package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Commands that forward flags to kubectl must parse their own flags by hand.
//
// Typer keeps unrecognized options in ctx.args via
// `{"allow_extra_args": True, "ignore_unknown_options": True}`, which is what
// makes `kx get pods -n prod -l app=web` work. Cobra has no equivalent:
// FParseErrWhitelist{UnknownFlags: true} *discards* unknown flags rather than
// passing them through, so `-n prod` would silently vanish and the listing
// would be saved against the wrong namespace. Those commands set
// DisableFlagParsing and use the helpers below, which remove only kx's own
// flags and leave everything else untouched for kubectl.

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
				return nil, nil, fmt.Errorf("flag needs an argument: %s", arg)
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
// This must recognise exactly the spellings extractString consumes,
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

// refuseScopeFlag rejects a namespace-scope flag in args that would contradict
// a resolved index, given the namespace that index resolved to.
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
