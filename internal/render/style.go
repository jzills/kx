package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette style specs are Rich-style strings: a hex color, a named terminal
// color, the literal "default", and an optional "bold" attribute — e.g.
// "#3fb950", "bold #3fb950", "bright_black", "bold", "default".
//
// namedColors maps the names the palettes use onto ANSI indexes. Only the names
// actually used need to be here; anything unrecognized falls through to the
// terminal default rather than erroring, since a palette is data, not code.
var namedColors = map[string]string{
	"black":          "0",
	"red":            "1",
	"green":          "2",
	"yellow":         "3",
	"blue":           "4",
	"magenta":        "5",
	"cyan":           "6",
	"white":          "7",
	"bright_black":   "8",
	"bright_red":     "9",
	"bright_green":   "10",
	"bright_yellow":  "11",
	"bright_blue":    "12",
	"bright_magenta": "13",
	"bright_cyan":    "14",
	"bright_white":   "15",
}

// parseStyle turns a palette spec into a lipgloss style built by the given
// renderer, so the renderer's color profile (and NO_COLOR / non-TTY
// degradation) applies.
func parseStyle(renderer *lipgloss.Renderer, spec string) lipgloss.Style {
	style := renderer.NewStyle()
	for _, token := range strings.Fields(spec) {
		switch {
		case token == "bold":
			style = style.Bold(true)
		case token == "default":
			// The terminal's own foreground: no color set.
		case strings.HasPrefix(token, "#"):
			style = style.Foreground(lipgloss.Color(token))
		default:
			if ansi, ok := namedColors[token]; ok {
				style = style.Foreground(lipgloss.Color(ansi))
			}
		}
	}
	return style
}
