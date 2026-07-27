// Command kx wraps kubectl with index-based resource selection.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jzills/kx/internal/cli"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/kx
var version = "dev"

func main() {
	os.Exit(run())
}

// run renders expected failures as a styled error and returns the exit code,
// so no path calls os.Exit while defers are pending.
//
// Config errors are already user-facing (they carry the "kx: " prefix) and
// print verbatim; command errors go through the renderer, matching the Python
// handle_errors decorator.
func run() int {
	loader := config.Loader{}
	cfg, err := loader.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	root := cli.NewRoot(cli.NewServices(cfg), version)
	if err := root.Execute(); err != nil {
		var silent cli.SilentError
		if errors.As(err, &silent) {
			return silent.Code
		}
		render.Error(err.Error())
		return 1
	}
	return 0
}
