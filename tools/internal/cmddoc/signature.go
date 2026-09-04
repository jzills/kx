// Package cmddoc renders a command's signature the one way the generated
// documentation spells it.
//
// Two generators need it — tools/gen-command-table for the README's table and
// tools/gen-site-docs for the site's — and two copies would eventually disagree
// about how to write `kx get <resource> [--match/-m str]`, which is the drift
// generating them from the command tree exists to prevent.
package cmddoc

import (
	"strings"

	"github.com/jzills/kx/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ArgToken renders a positional argument: angle brackets name it, square
// brackets mark it optional, an ellipsis marks it repeatable.
func ArgToken(arg cli.Arg) string {
	name := "<" + arg.Name + ">"
	if arg.Variadic {
		name += "..."
	}
	if arg.Required {
		return name
	}
	return "[" + name + "]"
}

// FlagToken renders a flag as [--long/-short type], dropping the type for
// booleans since a switch takes no value.
func FlagToken(flag *pflag.Flag) string {
	names := "--" + flag.Name
	if flag.Shorthand != "" {
		names += "/-" + flag.Shorthand
	}
	switch flag.Value.Type() {
	case "bool":
		return "[" + names + "]"
	case "string", "stringArray", "stringSlice":
		return "[" + names + " str]"
	default:
		return "[" + names + " " + flag.Value.Type() + "]"
	}
}

// Signature is the whole invocation: the command, its positional arguments, its
// own flags, and a pass-through placeholder if it forwards any.
func Signature(cmd *cobra.Command) string {
	spec := cli.ParseUse(cmd.Use)
	parts := []string{"kx " + cmd.Name()}
	for _, arg := range spec.Args {
		parts = append(parts, ArgToken(arg))
	}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// Inherited flags are global (--no-color) and belong on the root's help,
		// not on every row of the table.
		if flag.Hidden || flag.Name == "help" || cmd.InheritedFlags().Lookup(flag.Name) != nil {
			return
		}
		parts = append(parts, FlagToken(flag))
	})
	if spec.Passthrough != "" {
		parts = append(parts, "["+spec.Passthrough+"...]")
	}
	return strings.Join(parts, " ")
}

// ShortSignature is the invocation without its flags: the command and its
// positional arguments only. The README's table uses it so the command column
// stays narrow enough to scan — the full flag list belongs on the site's
// reference pages, which render one command per page and have room for it.
func ShortSignature(cmd *cobra.Command) string {
	spec := cli.ParseUse(cmd.Use)
	parts := []string{"kx " + cmd.Name()}
	for _, arg := range spec.Args {
		parts = append(parts, ArgToken(arg))
	}
	return strings.Join(parts, " ")
}

// Commands returns every registered command keyed by name, so a generator can
// walk cli.CommandOrder rather than the tree's own arbitrary order.
func Commands() map[string]*cobra.Command {
	byName := map[string]*cobra.Command{}
	for _, cmd := range cli.NewRoot(cli.Services{}, "dev").Commands() {
		byName[cmd.Name()] = cmd
	}
	return byName
}
