package cli

import (
	"context"
	"slices"

	"github.com/spf13/cobra"
)

// withRefresh wraps a command so that a failure caused by a stale index
// re-runs the listing the index came from and renders it, letting the user pick
// a new index instead of having to remember what they ran.
//
// The original command is never retried: the index→name mapping may have
// shifted, so a retry could act on a different resource than the one asked for.
func withRefresh(services Services, cmd *cobra.Command) *cobra.Command {
	inner := cmd.RunE
	if inner == nil {
		return cmd
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		err := inner(c, args)
		if err == nil || !isStale(err) {
			return err
		}
		// Reported here, rather than by the entrypoint, so the refreshed
		// listing lands under the failure that caused it. SilentError tells
		// the entrypoint the failure has already reached the user.
		if machineOutput(c, args) {
			reportStale(services, err)
			return SilentError{Code: 1}
		}
		ctx := c.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		handleStale(ctx, services, err)
		return SilentError{Code: 1}
	}
	return cmd
}

// machineOutput reports whether an invocation asked for output a program
// reads rather than a person: kx's own --json, or a kubectl -o format that is
// not a table (see printsTable). A refreshed listing is a table for a person
// to pick from, and it goes to stdout, where a script reading the document
// fails to parse it.
//
// A command that parses its own flags has them in argv. Only those before a
// "--" are read: what follows is a command for a container, and an -o there
// is that command's.
func machineOutput(c *cobra.Command, args []string) bool {
	if flag := c.Flags().Lookup("json"); flag != nil && flag.Changed {
		return true
	}
	if !c.DisableFlagParsing {
		return false
	}
	if end := slices.Index(args, "--"); end >= 0 {
		args = args[:end]
	}
	return hasFlag(args, "--json", "") || !printsTable(args)
}

// withoutRefresh marks a command whose failures never mean stale state.
func withoutRefresh(cmd *cobra.Command) *cobra.Command { return cmd }
