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
//
// The signature rendering lives in tools/internal/cmddoc, shared with
// gen-site-docs so the README and the site spell a command the same way.
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jzills/kx/internal/cli"
	"github.com/jzills/kx/tools/internal/cmddoc"
)

const (
	sentinelStart = "<!-- commands-table-start -->"
	sentinelEnd   = "<!-- commands-table-end -->"
	readmePath    = "README.md"
)

func table() (string, error) {
	byName := cmddoc.Commands()

	rows := []string{"| Command | Description |", "|---|---|"}
	for _, name := range cli.CommandOrder() {
		cmd, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("command %q is listed in the help sections but not registered", name)
		}
		description := strings.Join(strings.Fields(cmd.Short), " ")
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", cmddoc.ShortSignature(cmd), description))
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
	// Literal: ReplaceAll expands $name and ${name} in the replacement, so a
	// command whose Short or Example mentions a shell variable — $EDITOR,
	// $KUBECONFIG — would render as an empty expansion in the README, with
	// nothing failing to say so.
	updated := pattern.ReplaceAllLiteral(content,
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
