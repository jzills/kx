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
