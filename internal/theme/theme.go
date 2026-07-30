// Package theme holds the prefab color palettes and expands them into the
// semantic style names the renderer uses.
//
// Render code refers to styles by meaning ("header", "status.ok") and never to
// a hex value, so a new palette changes every command's appearance without
// touching render code.
package theme

import "fmt"

// Palette is one prefab color scheme. Header defaults to bold accent.
type Palette struct {
	Accent  string
	Muted   string
	Body    string
	Error   string
	Warn    string
	Success string
	// Header is optional; empty means "bold " + Accent.
	Header string
}

// Default is the palette used when none is configured.
const Default = "github-dark"

// order is the display order for `kx theme`. Go maps don't preserve insertion
// order, so the sequence Python's dict gave for free is explicit here.
var order = []string{
	"github-dark",
	"dracula",
	"nord",
	"gruvbox",
	"solarized-dark",
	"catppuccin-mocha",
	"tokyo-night",
	"rose-pine",
	"mono",
	"light",
	"plain",
}

var palettes = map[string]Palette{
	"github-dark": {
		Accent: "#3fb950", Muted: "#7d8590", Body: "#e6edf3",
		Error: "#f85149", Warn: "#e3b341", Success: "#3fb950",
	},
	"dracula": {
		Accent: "#bd93f9", Muted: "#6272a4", Body: "#f8f8f2",
		Error: "#ff5555", Warn: "#f1fa8c", Success: "#50fa7b",
	},
	"nord": {
		Accent: "#88c0d0", Muted: "#4c566a", Body: "#d8dee9",
		Error: "#bf616a", Warn: "#ebcb8b", Success: "#a3be8c",
	},
	"gruvbox": {
		Accent: "#fe8019", Muted: "#928374", Body: "#ebdbb2",
		Error: "#fb4934", Warn: "#fabd2f", Success: "#b8bb26",
	},
	"solarized-dark": {
		Accent: "#268bd2", Muted: "#586e75", Body: "#93a1a1",
		Error: "#dc322f", Warn: "#b58900", Success: "#859900",
	},
	"catppuccin-mocha": {
		Accent: "#cba6f7", Muted: "#6c7086", Body: "#cdd6f4",
		Error: "#f38ba8", Warn: "#f9e2af", Success: "#a6e3a1",
	},
	"tokyo-night": {
		Accent: "#7aa2f7", Muted: "#565f89", Body: "#c0caf5",
		Error: "#f7768e", Warn: "#e0af68", Success: "#9ece6a",
	},
	"rose-pine": {
		Accent: "#c4a7e7", Muted: "#6e6a86", Body: "#e0def4",
		Error: "#eb6f92", Warn: "#f6c177", Success: "#9ccfd8",
	},
	"mono": {
		Accent: "bold", Muted: "bright_black", Body: "default",
		Error: "bold", Warn: "default", Success: "bold", Header: "bold",
	},
	"light": {
		Accent: "#1a7f37", Muted: "#57606a", Body: "#24292f",
		Error: "#cf222e", Warn: "#9a6700", Success: "#1a7f37",
	},
	// Every style resolves to the terminal default: no colors, no attributes.
	"plain": {
		Accent: "default", Muted: "default", Body: "default",
		Error: "default", Warn: "default", Success: "default", Header: "default",
	},
}

// Semantic style names. Render code uses these; nothing outside this package
// refers to a palette field directly.
const (
	Accent        = "accent"
	Header        = "header"
	Muted         = "muted"
	Body          = "body"
	Error         = "error"
	Warn          = "warn"
	Success       = "success"
	StatusOK      = "status.ok"
	StatusWarn    = "status.warn"
	StatusBad     = "status.bad"
	StatusNeutral = "status.neutral"
)

// Names returns the theme names in display order.
func Names() []string {
	names := make([]string, len(order))
	copy(names, order)
	return names
}

// Exists reports whether name is a known theme.
func Exists(name string) bool {
	_, ok := palettes[name]
	return ok
}

// Styles expands a palette into the full semantic style mapping.
func Styles(name string) (map[string]string, error) {
	palette, ok := palettes[name]
	if !ok {
		return nil, fmt.Errorf("Unknown theme '%s'. Run 'kx theme' to list themes.", name)
	}
	header := palette.Header
	if header == "" {
		header = "bold " + palette.Accent
	}
	return map[string]string{
		Accent:        palette.Accent,
		Header:        header,
		Muted:         palette.Muted,
		Body:          palette.Body,
		Error:         palette.Error,
		Warn:          palette.Warn,
		Success:       palette.Success,
		StatusOK:      palette.Success,
		StatusWarn:    palette.Warn,
		StatusBad:     palette.Error,
		StatusNeutral: palette.Body,
	}, nil
}

// MustStyles is Styles for a name already known to be valid.
func MustStyles(name string) map[string]string {
	styles, err := Styles(name)
	if err != nil {
		panic(err)
	}
	return styles
}
