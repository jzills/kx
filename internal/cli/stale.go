package cli

import "github.com/spf13/cobra"

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
		handleStale(services, err)
		return SilentError{Code: 1}
	}
	return cmd
}

// withoutRefresh marks a command whose failures never mean stale state.
func withoutRefresh(cmd *cobra.Command) *cobra.Command { return cmd }
