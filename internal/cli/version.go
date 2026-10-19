package cli

import (
	"runtime"
	"sync"
	"weak"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/buildinfo"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/kubectlflags"
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
//
// Two lines name kubectl, because kx's flag handling is read from one kubectl
// release and runs another: kubectl is the binary on PATH that kx runs, flags
// the release kx's table of kubectl's flags was generated from. When a flag
// newer than the table is refused, the pair says why.
func versionText(info buildinfo.Info, client kubectl.Client) string {
	detail := [][2]string{}
	if info.Commit != "" {
		detail = append(detail, [2]string{"commit", info.Commit})
	}
	if info.Date != "" {
		detail = append(detail, [2]string{"built", info.Date})
	}
	detail = append(detail, [2]string{"go", info.Go + " " + info.Platform})
	detail = append(detail, [2]string{"kubectl", clientText(client)})
	detail = append(detail, [2]string{"flags", "kubectl " + kubectlflags.Version})
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

// clientText is the kubectl line: the version and where it was found. A missing
// kubectl is reported rather than left out, since --version is where someone
// checking their setup learns that.
func clientText(client kubectl.Client) string {
	switch {
	case client.Path == "":
		return "not found on PATH"
	case client.Version == "":
		return homeRelative(client.Path) + " (version unknown)"
	default:
		return client.Version + " " + homeRelative(client.Path)
	}
}

// versionTexts maps each root command to what its --version prints.
//
// Cobra prints --version by executing a template, and the template is fixed
// when the command is built. Rendering the text then would run kubectl on every
// kx invocation for a line almost none of them print. Instead the template
// calls kxVersion, which renders the text only when --version is given.
//
// Keyed by root because a template function is registered once, process-wide,
// while tests build many roots with different services. Weakly, and dropped
// when the root is collected: a strong key kept every root a test built, and
// its whole command tree, reachable for the rest of the run.
var versionTexts sync.Map // weak.Pointer[cobra.Command] → func() string

func init() {
	cobra.AddTemplateFunc("kxVersion", func(cmd *cobra.Command) string {
		text, ok := versionTexts.Load(weak.Make(cmd.Root()))
		if !ok {
			return ""
		}
		return text.(func() string)()
	})
}

// installVersion sets root's --version to versionText, rendered on demand.
//
// The text is returned by a template function rather than set as the template:
// cobra parses a version template as text/template, so a config or state path
// containing "{{" would be executed as an action.
func installVersion(root *cobra.Command, info buildinfo.Info, detect func() kubectl.Client) {
	if detect == nil {
		detect = kubectl.DetectClient
	}
	key := weak.Make(root)
	versionTexts.Store(key, func() string { return versionText(info, detect()) })
	runtime.AddCleanup(root, func(key weak.Pointer[cobra.Command]) { versionTexts.Delete(key) }, key)
	root.SetVersionTemplate("{{kxVersion .}}")
}
