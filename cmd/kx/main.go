// Command kx wraps kubectl with index-based resource selection.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jzills/kx/internal/cli"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/discovery"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
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

// fixesUnknownName lets kx theme run despite an unknown configured theme, and
// kx engine despite an unknown engine, on the default in its place. The error
// sends the user to that command, and refusing it too left editing the file
// by hand as the only way out. Every other command still refuses.
func fixesUnknownName(err error, cfg *config.Config, args []string) bool {
	var unknown config.UnknownNameError
	if !errors.As(err, &unknown) || firstPositional(args) != unknown.Setting {
		return false
	}
	switch unknown.Setting {
	case "theme":
		cfg.Theme = theme.Default
	case "engine":
		cfg.Engine = config.DefaultEngine
	}
	return true
}

// firstPositional is the first argument that is not a flag: the command name,
// since kx's root flags take no value.
func firstPositional(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// completionHelper is the name kubectl runs to complete a plugin's
// arguments: `kubectl idx get pods <TAB>` asks kubectl_complete-idx, on the
// PATH, with the arguments after idx.
//
// That is also where the krew alias's completion goes. zsh expands an alias
// before completing it, so `kx` there is `kubectl idx` and kx's own zsh
// script is never consulted; with no helper, kubectl answered with the
// shell's file completion, and `kx get pods <TAB>` listed the directory. A
// link to kx under this name answers as `kx __complete` does.
const completionHelper = "kubectl_complete-idx"

// argsOf is the command line kx runs: its arguments, or under the
// completionHelper name, a completion of them.
func argsOf(argv []string) []string {
	name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe")
	if name == completionHelper {
		return append([]string{"__complete"}, argv[1:]...)
	}
	return argv[1:]
}

// run renders expected failures as a styled error and returns the exit code,
// so no path calls os.Exit while defers are pending.
//
// Config errors go through the renderer like any other failure, styled with
// the default theme: the configured one is what failed to load.
func run() int {
	args := argsOf(os.Args)
	loader := config.Loader{ThemeKnown: theme.Exists, EngineKnown: scanner.Exists}
	cfg, err := loader.Load()
	if err != nil && !fixesUnknownName(err, &cfg, args) {
		render.Configure(theme.Default, hasNoColorFlag(args))
		render.Error(err.Error())
		return 1
	}

	// --no-color is resolved from the raw arguments rather than from cobra,
	// because the pass-through commands disable flag parsing and would
	// otherwise not surface it until after their first output.
	render.Configure(cfg.Theme, cfg.ThemeDisable || hasNoColorFlag(args))

	// Installed here rather than in cli.NewRoot so it only ever runs for the
	// real binary: internal/cli's own tests call NewRoot directly, many
	// times, and a real discovery.Source reads the ambient kubeconfig — on
	// a kubeconfig with an exec-credential plugin, that would execute the
	// plugin during `go test ./internal/cli/...`.
	kinds.SetShorthandSource(discovery.NewSource())
	root := cli.NewRoot(cli.NewServices(cfg), version)
	if err := cli.Execute(root, args); err != nil {
		var silent cli.SilentError
		if errors.As(err, &silent) {
			return silent.Code
		}
		// Reported here rather than by the command, so the exit code survives
		// alongside a message kx has to print itself because kubectl's was
		// suppressed.
		var exit cli.ExitError
		if errors.As(err, &exit) {
			render.Error(exit.Message)
			return exit.Code
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
