// Command kx wraps kubectl with index-based resource selection.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jzills/kx/internal/cli"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/theme"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/kx
var version = "dev"

func main() {
	os.Exit(run())
}

func hasNoColorFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--no-color" {
			return true
		}
	}
	return false
}

// run renders expected failures as a styled error and returns the exit code,
// so no path calls os.Exit while defers are pending.
//
// Config errors are already user-facing (they carry the "kx: " prefix) and
// print verbatim; command errors go through the renderer, matching the Python
// handle_errors decorator.
func run() int {
	loader := config.Loader{ThemeKnown: theme.Exists}
	cfg, err := loader.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// --no-color is resolved from the raw arguments rather than from cobra,
	// because the pass-through commands disable flag parsing and would
	// otherwise not surface it until after their first output.
	render.Configure(cfg.Theme, cfg.NoColor || hasNoColorFlag(os.Args[1:]))

	root := cli.NewRoot(cli.NewServices(cfg), version)
	if err := cli.Execute(root, os.Args[1:]); err != nil {
		var silent cli.SilentError
		if errors.As(err, &silent) {
			return silent.Code
		}
		// A declined confirmation is the user's decision, not a failure to
		// report back to them with an error marker.
		var aborted render.ErrAborted
		if errors.As(err, &aborted) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return 1
		}
		render.Error(err.Error())
		return 1
	}
	return 0
}
