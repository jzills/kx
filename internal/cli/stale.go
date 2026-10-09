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

// documentAnnotation marks a command whose stdout is a document a program
// reads however it was invoked — a manifest, a log stream, whatever ran
// inside a container — rather than a table for a person. Those commands
// produce one without anyone typing a format, so machineOutput cannot infer
// it from the arguments the way it can for -o or --json.
//
// kx cp does not carry it: kubectl cp writes files, and its stdout is its
// own progress, not the copy.
const documentAnnotation = "kx.document"

// documentAnnotations is the Command.Annotations value for a document
// command, and mutatingDocumentAnnotations for one that also spends its index
// on the cluster. Shared maps, for the reason mutatingAnnotations is one:
// nothing writes to a command's Annotations after construction.
var (
	documentAnnotations         = map[string]string{documentAnnotation: "true"}
	mutatingDocumentAnnotations = map[string]string{
		mutatingAnnotation: "true", documentAnnotation: "true",
	}
)

// machineOutput reports whether an invocation's stdout is read by a program
// rather than a person: a command whose output is always a document
// (documentAnnotation), kx's own --json, one Secret value written raw by
// --decode --key, or a kubectl -o format that is not a table (see
// printsTable). A refreshed listing is a table for a person to pick from, and
// it goes to stdout, where a script reading the document fails to parse it —
// `OUT=$(kx exec 3 -- cat /etc/hostname)` captured the table — or, under
// $(kx secret 1 --decode -k token), exports it as the credential.
//
// A command that parses its own flags has them in argv. Only those before a
// "--" are read: what follows is a trailing command, and an -o there is that
// command's. kx exec and kx debug, which are what put one there, are
// documents outright and never reach it — the trim guards any other
// passthrough command whose argv carries a "--".
func machineOutput(c *cobra.Command, args []string) bool {
	if c.Annotations[documentAnnotation] == "true" {
		return true
	}
	if flag := c.Flags().Lookup("json"); flag != nil && flag.Changed {
		return true
	}
	if !c.DisableFlagParsing {
		return false
	}
	if end := slices.Index(args, "--"); end >= 0 {
		args = args[:end]
	}
	// --decode is kx's alone, so its -k is the key and not kubectl's -k
	// (kustomize), which other commands pass through.
	if decode, _ := extractBool(args, "--decode"); decode && hasFlag(args, "--key", "-k") {
		return true
	}
	return hasFlag(args, "--json", "") || !printsTable(args)
}

// withoutRefresh marks a command whose failures never mean stale state.
func withoutRefresh(cmd *cobra.Command) *cobra.Command { return cmd }
