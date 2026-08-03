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

	Chrome Chrome
}

// Chrome is the page-level colour a terminal palette has no need for: a
// terminal supplies its own background, a browser does not.
//
// The foreground fields are overrides, set only by palettes whose terminal
// specs are attributes rather than colours. "bold" and "bright_black" mean
// something to a terminal and nothing to CSS, so those palettes name explicit
// web colours here instead.
type Chrome struct {
	Background string
	Surface    string
	Border     string

	Accent  string
	Muted   string
	Body    string
	Error   string
	Warn    string
	Success string
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
		Chrome: Chrome{Background: "#0d1117", Surface: "#161b22", Border: "#30363d"},
	},
	"dracula": {
		Accent: "#bd93f9", Muted: "#6272a4", Body: "#f8f8f2",
		Error: "#ff5555", Warn: "#f1fa8c", Success: "#50fa7b",
		Chrome: Chrome{Background: "#282a36", Surface: "#343746", Border: "#44475a"},
	},
	"nord": {
		Accent: "#88c0d0", Muted: "#4c566a", Body: "#d8dee9",
		Error: "#bf616a", Warn: "#ebcb8b", Success: "#a3be8c",
		// Nord's terminal muted is its own border colour, which would vanish
		// against the page. The web muted is lifted one step up the Nord ramp.
		Chrome: Chrome{Background: "#2e3440", Surface: "#3b4252", Border: "#4c566a",
			Muted: "#7b88a1"},
	},
	"gruvbox": {
		Accent: "#fe8019", Muted: "#928374", Body: "#ebdbb2",
		Error: "#fb4934", Warn: "#fabd2f", Success: "#b8bb26",
		Chrome: Chrome{Background: "#282828", Surface: "#32302f", Border: "#504945"},
	},
	"solarized-dark": {
		Accent: "#268bd2", Muted: "#586e75", Body: "#93a1a1",
		Error: "#dc322f", Warn: "#b58900", Success: "#859900",
		Chrome: Chrome{Background: "#002b36", Surface: "#073642", Border: "#586e75",
			Muted: "#849496"},
	},
	"catppuccin-mocha": {
		Accent: "#cba6f7", Muted: "#6c7086", Body: "#cdd6f4",
		Error: "#f38ba8", Warn: "#f9e2af", Success: "#a6e3a1",
		Chrome: Chrome{Background: "#1e1e2e", Surface: "#313244", Border: "#45475a",
			Muted: "#8b90a5"},
	},
	"tokyo-night": {
		Accent: "#7aa2f7", Muted: "#565f89", Body: "#c0caf5",
		Error: "#f7768e", Warn: "#e0af68", Success: "#9ece6a",
		Chrome: Chrome{Background: "#1a1b26", Surface: "#24283b", Border: "#414868",
			Muted: "#787f9c"},
	},
	"rose-pine": {
		Accent: "#c4a7e7", Muted: "#6e6a86", Body: "#e0def4",
		Error: "#eb6f92", Warn: "#f6c177", Success: "#9ccfd8",
		Chrome: Chrome{Background: "#191724", Surface: "#1f1d2e", Border: "#403d52",
			Muted: "#908caa"},
	},
	"mono": {
		Accent: "bold", Muted: "bright_black", Body: "default",
		Error: "bold", Warn: "default", Success: "bold", Header: "bold",
		// Every terminal value here is an attribute, so the whole web palette
		// is declared. Severity stays legible without hue: the page marks it
		// with an icon and a row stripe as well as a colour.
		Chrome: Chrome{
			Background: "#101010", Surface: "#1a1a1a", Border: "#333333",
			Accent: "#ffffff", Muted: "#8a8a8a", Body: "#e6e6e6",
			Error: "#ffffff", Warn: "#c0c0c0", Success: "#bdbdbd",
		},
	},
	"light": {
		Accent: "#1a7f37", Muted: "#57606a", Body: "#24292f",
		Error: "#cf222e", Warn: "#9a6700", Success: "#1a7f37",
		Chrome: Chrome{Background: "#ffffff", Surface: "#f6f8fa", Border: "#d0d7de"},
	},
	// Every style resolves to the terminal default: no colors, no attributes.
	"plain": {
		Accent: "default", Muted: "default", Body: "default",
		Error: "default", Warn: "default", Success: "default", Header: "default",
		// "No colour" has no CSS spelling, so the page borrows mono's neutral
		// ramp rather than rendering one unreadable colour on itself.
		Chrome: Chrome{
			Background: "#101010", Surface: "#1a1a1a", Border: "#333333",
			Accent: "#ffffff", Muted: "#8a8a8a", Body: "#e6e6e6",
			Error: "#ffffff", Warn: "#c0c0c0", Success: "#bdbdbd",
		},
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

	// Page-level names, used only by WebStyles. A terminal has no use for
	// them: it supplies its own background and draws no borders.
	Background = "background"
	Surface    = "surface"
	Border     = "border"
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

// WebStyles expands a palette into the CSS custom properties a page sets, as
// semantic name → #rrggbb.
//
// Unlike Styles, every value is a literal colour. Styles may return terminal
// attributes ("bold", "bright_black", "default"), which CSS cannot express, so
// palettes built from those declare explicit web colours in their Chrome.
//
// header is deliberately absent: Styles builds it as "bold " + Accent, and
// boldness is a font weight in CSS rather than a colour. The stylesheet sets
// the weight itself and draws headings in the accent.
func WebStyles(name string) (map[string]string, error) {
	palette, ok := palettes[name]
	if !ok {
		return nil, fmt.Errorf("Unknown theme '%s'. Run 'kx theme' to list themes.", name)
	}
	pick := func(override, terminal string) string {
		if override != "" {
			return override
		}
		return terminal
	}
	accent := pick(palette.Chrome.Accent, palette.Accent)
	muted := pick(palette.Chrome.Muted, palette.Muted)
	body := pick(palette.Chrome.Body, palette.Body)
	bad := pick(palette.Chrome.Error, palette.Error)
	warn := pick(palette.Chrome.Warn, palette.Warn)
	success := pick(palette.Chrome.Success, palette.Success)

	return map[string]string{
		Accent:  accent,
		Muted:   muted,
		Body:    body,
		Error:   bad,
		Warn:    warn,
		Success: success,
		// The status.* names alias the three severity colours, exactly as
		// Styles does, so render.StatusStyle's answers resolve here too.
		StatusOK:      success,
		StatusWarn:    warn,
		StatusBad:     bad,
		StatusNeutral: body,
		Background:    palette.Chrome.Background,
		Surface:       palette.Chrome.Surface,
		Border:        palette.Chrome.Border,
	}, nil
}
