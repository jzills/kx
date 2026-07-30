package cli

import (
	"fmt"
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
// "--long value", "--long=value", "-s value" and "-s=value". A flag given more
// than once keeps the last value, matching pflag.
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
func hasFlag(args []string, long, short string) bool {
	for _, arg := range args {
		switch {
		case arg == long, short != "" && arg == short:
			return true
		case strings.HasPrefix(arg, long+"="):
			return true
		case short != "" && strings.HasPrefix(arg, short+"="):
			return true
		}
	}
	return false
}

// extractBool removes a boolean flag from args and reports whether it was
// present.
func extractBool(args []string, names ...string) (present bool, rest []string) {
	rest = make([]string, 0, len(args))
	for _, arg := range args {
		matched := false
		for _, name := range names {
			if arg == name {
				matched = true
				break
			}
		}
		if matched {
			present = true
			continue
		}
		rest = append(rest, arg)
	}
	return present, rest
}
