// Command gen-command-table regenerates the command reference table in
// README.md from the CLI definition, so the table can't drift from the commands
// it documents.
//
// Run by the gen-command-table pre-commit hook:
//
//	go run ./tools/gen-command-table
//
// Exits non-zero when it rewrote the table, so the hook surfaces the change for
// staging rather than committing a stale README.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jzills/kx/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	sentinelStart = "<!-- commands-table-start -->"
	sentinelEnd   = "<!-- commands-table-end -->"
	readmePath    = "README.md"
)

// argToken renders a positional argument the way the table shows it: angle
// brackets name it, square brackets mark it optional, an ellipsis marks it
// repeatable.
func argToken(arg cli.Arg) string {
	name := "<" + arg.Name + ">"
	if arg.Variadic {
		name += "..."
	}
	if arg.Required {
		return name
	}
	return "[" + name + "]"
}

// flagToken renders a flag as [--long/-short type], dropping the type for
// booleans since a switch takes no value.
func flagToken(flag *pflag.Flag) string {
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

func signature(cmd *cobra.Command) string {
	spec := cli.ParseUse(cmd.Use)
	parts := []string{"kx " + cmd.Name()}
	for _, arg := range spec.Args {
		parts = append(parts, argToken(arg))
	}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		// Inherited flags are global (--no-color) and belong on the root's help,
		// not on every row of the table.
		if flag.Hidden || flag.Name == "help" || cmd.InheritedFlags().Lookup(flag.Name) != nil {
			return
		}
		parts = append(parts, flagToken(flag))
	})
	if spec.Passthrough != "" {
		parts = append(parts, "["+spec.Passthrough+"...]")
	}
	return strings.Join(parts, " ")
}

func table() (string, error) {
	byName := map[string]*cobra.Command{}
	for _, cmd := range cli.NewRoot(cli.Services{}, "dev").Commands() {
		byName[cmd.Name()] = cmd
	}

	rows := []string{"| Command | Description |", "|---|---|"}
	for _, name := range cli.CommandOrder() {
		cmd, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("command %q is listed in the help sections but not registered", name)
		}
		description := strings.Join(strings.Fields(cmd.Short), " ")
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", signature(cmd), description))
	}
	return strings.Join(rows, "\n"), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	content, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}

	pattern := regexp.MustCompile(
		"(?s)" + regexp.QuoteMeta(sentinelStart) + ".*?" + regexp.QuoteMeta(sentinelEnd))
	if !pattern.Match(content) {
		return fmt.Errorf("sentinel comments not found in %s; add:\n  %s\n  %s",
			readmePath, sentinelStart, sentinelEnd)
	}

	rendered, err := table()
	if err != nil {
		return err
	}
	updated := pattern.ReplaceAll(content,
		[]byte(sentinelStart+"\n"+rendered+"\n"+sentinelEnd))

	if string(updated) == string(content) {
		fmt.Printf("%s is up to date.\n", readmePath)
		return nil
	}
	if err := os.WriteFile(readmePath, updated, 0o644); err != nil {
		return err
	}
	// Non-zero so pre-commit surfaces the rewrite for staging.
	return fmt.Errorf("updated %s", readmePath)
}
