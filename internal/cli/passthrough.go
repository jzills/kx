package cli

import (
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
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == long || (short != "" && arg == short):
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			value = args[i+1]
			i++
		case strings.HasPrefix(arg, long+"="):
			value = strings.TrimPrefix(arg, long+"=")
		case short != "" && strings.HasPrefix(arg, short+"="):
			value = strings.TrimPrefix(arg, short+"=")
		// Attached shorthand, e.g. "-nprod". Checked last and guarded on
		// length so a bare "-n" still falls into the exact-match case above
		// and takes the following argument instead of being trimmed to "".
		case short != "" && len(arg) > len(short) && strings.HasPrefix(arg, short):
			value = strings.TrimPrefix(arg, short)
		default:
			rest = append(rest, arg)
		}
	}
	return value, rest, nil
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
