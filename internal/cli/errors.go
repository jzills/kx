package cli

import "fmt"

// SilentError carries an exit code for a failure that has already been
// reported, so the entrypoint exits without rendering a second message.
//
// This is the Go counterpart to typer.Exit passing through handle_errors:
// interactive commands forward kubectl's own exit code after kubectl has
// already written to the terminal.
type SilentError struct {
	Code int
}

func (e SilentError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// ExitError is a failure kx has to report itself, exiting with a specific code
// rather than a flat 1.
//
// Used where kubectl's own stderr is suppressed — `kx exec 1 -- cmd` hides it,
// because a failing command inside the container produces a confusing "command
// terminated with exit code N" on top of whatever the command already printed.
// The message still has to reach the user, and the container's exit code still
// has to reach the shell.
type ExitError struct {
	Code    int
	Message string
}

func (e ExitError) Error() string { return e.Message }
