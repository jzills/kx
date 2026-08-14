package cli

import (
	"github.com/jzills/kx/internal/buildinfo"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// docsURL is where the long-form documentation lives. Printed by --version so
// a binary found on a machine can say where it came from.
//
// The documentation site rather than the repository: this line is read by
// someone who wants to know how to use the binary in front of them, and the
// repository answers that with a README and a source tree. It points at /docs/
// rather than the landing page for the same reason.
const docsURL = "https://jzills.github.io/kx/docs/"

// versionText is what `kx --version` prints.
//
// The first line is exactly "kx v<version>" and nothing else, because that is
// what the release workflow asserts against. It carries the whole version,
// pseudo-version tail and all — this is where "which build am I running" gets
// answered, so nothing is abbreviated away. Everything below it is detail for
// a bug report: which build, from which source, against which config.
// The version line itself is deliberately unstyled: it is the compatibility
// surface above, and escape codes around it would reach anything that captures
// this output on a terminal. Everything below is kx's own reading matter and
// goes through the renderer like the rest of the CLI.
func versionText(info buildinfo.Info) string {
	detail := [][2]string{}
	if info.Commit != "" {
		detail = append(detail, [2]string{"commit", info.Commit})
	}
	if info.Date != "" {
		detail = append(detail, [2]string{"built", info.Date})
	}
	detail = append(detail, [2]string{"go", info.Go + " " + info.Platform})
	// Both paths are resolved from the packages that own them, so this can
	// never name a file kx does not use — and a home directory kx cannot
	// locate drops the line rather than guessing at it. Same rule, and the
	// same pairing, as the help screen's Files block.
	if path, err := config.File(); err == nil {
		detail = append(detail, [2]string{"config", homeRelative(path)})
	}
	if path, err := state.File(); err == nil {
		detail = append(detail, [2]string{"state", homeRelative(path)})
	}
	detail = append(detail, [2]string{"docs", docsURL})

	return "kx " + info.Tag() + "\n" + render.Detail(detail) + "\n"
}
