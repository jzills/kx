package cli

import (
	"strings"

	"github.com/jzills/kx/internal/buildinfo"
	"github.com/jzills/kx/internal/config"
)

// docsURL is where the long-form documentation lives. Printed by --version so
// a binary found on a machine can say where it came from.
const docsURL = "https://github.com/jzills/kx"

// versionText is what `kx --version` prints.
//
// The first line is exactly "kx <version>" and nothing else, because that is
// what the release workflow asserts against and what any script parsing this
// output would have been written against. Everything below it is detail for a
// bug report: which build, from which source, against which config.
func versionText(info buildinfo.Info) string {
	lines := []string{"kx " + info.Version}

	detail := [][2]string{}
	if info.Commit != "" {
		detail = append(detail, [2]string{"commit", info.Commit})
	}
	if info.Date != "" {
		detail = append(detail, [2]string{"built", info.Date})
	}
	detail = append(detail, [2]string{"go", info.Go + " " + info.Platform})
	if path, err := config.File(); err == nil {
		detail = append(detail, [2]string{"config", homeRelative(path)})
	}
	detail = append(detail, [2]string{"docs", docsURL})

	width := 0
	for _, row := range detail {
		if len(row[0]) > width {
			width = len(row[0])
		}
	}
	for _, row := range detail {
		lines = append(lines, "  "+row[0]+strings.Repeat(" ", width-len(row[0]))+"  "+row[1])
	}
	return strings.Join(lines, "\n") + "\n"
}
