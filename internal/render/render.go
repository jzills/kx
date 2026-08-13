// Package render writes kx output through a themed console.
//
// Every command renders through the package-level renderer, and refers to
// styles by meaning ("header", "status.ok") rather than by color, so swapping a
// theme changes the whole CLI without touching render code.
package render

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/theme"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

const headerStyle = theme.Header

// Renderer holds the resolved styles and the streams to write them to.
type Renderer struct {
	out io.Writer
	err io.Writer
	// lip carries the color profile, so styles built later (theme previews)
	// degrade the same way the active theme's do.
	lip    *lipgloss.Renderer
	styles map[string]lipgloss.Style
	// answers buffers the confirmation prompt's input stream. Held on the
	// renderer so consecutive prompts share one buffer; see prompts().
	answers *bufio.Reader
}

var current = New(os.Stdout, os.Stderr, theme.Default, false)

// New builds a renderer for the given streams.
//
// Styling is emitted only when it is wanted and can be seen: plain forces it
// off (the --no-color flag and the no_color config key), NO_COLOR is honored
// per the convention, and a non-terminal stdout is left unstyled so piped or
// redirected output stays clean for grep and awk.
func New(out, errOut io.Writer, themeName string, plain bool) *Renderer {
	profile := termenv.Ascii
	if !plain && os.Getenv("NO_COLOR") == "" && isTerminal(out) {
		profile = termenv.EnvColorProfile()
	}
	return newWithProfile(out, errOut, themeName, profile)
}

// newWithProfile builds a renderer at an explicit color profile, so tests can
// exercise styled output without a terminal.
func newWithProfile(out, errOut io.Writer, themeName string, profile termenv.Profile) *Renderer {
	renderer := lipgloss.NewRenderer(out)
	renderer.SetColorProfile(profile)

	specs, err := theme.Styles(themeName)
	if err != nil {
		// An unknown theme is rejected at config load; anything reaching here
		// is a programming error, so fall back rather than fail a command.
		specs = theme.MustStyles(theme.Default)
	}
	return &Renderer{out: out, err: errOut, lip: renderer, styles: buildStyles(renderer, specs)}
}

func buildStyles(renderer *lipgloss.Renderer, specs map[string]string) map[string]lipgloss.Style {
	styles := make(map[string]lipgloss.Style, len(specs))
	for name, spec := range specs {
		styles[name] = parseStyle(renderer, spec)
	}
	return styles
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd())
}

// Configure swaps the package-level renderer, which is how the theme and
// --no-color flag take effect.
func Configure(themeName string, plain bool) {
	current = New(os.Stdout, os.Stderr, themeName, plain)
}

// SetOutput redirects the package-level renderer, used by tests. Output is
// unstyled because the streams are not terminals.
func SetOutput(out, errOut io.Writer, themeName string) {
	current = New(out, errOut, themeName, false)
}

func (r *Renderer) style(name, text string) string {
	style, ok := r.styles[name]
	if !ok {
		return text
	}
	return style.Render(text)
}

func (r *Renderer) write(text string) { fmt.Fprint(r.out, text) }

func (r *Renderer) line(text string) { fmt.Fprintln(r.out, text) }

// Success reports a completed action. Quoted fragments are accented so the
// resource a command acted on stands out from the sentence around it.
func (r *Renderer) Success(msg string) {
	r.line(r.style(theme.Success, "✓") + " " + r.emphasizeQuoted(msg, theme.Body))
}

// Error reports a failed command, on stderr so piped stdout stays clean.
func (r *Renderer) Error(msg string) {
	fmt.Fprintln(r.err, r.style(theme.Error, "✗")+" "+r.emphasizeQuoted(msg, theme.Body))
}

// emphasizeQuoted accents 'single-quoted' fragments within an otherwise
// uniformly styled message.
func (r *Renderer) emphasizeQuoted(msg, base string) string {
	parts := strings.Split(msg, "'")
	if len(parts) < 3 {
		return r.style(base, msg)
	}
	var out strings.Builder
	for i, part := range parts {
		// Odd indexes are the quoted fragments.
		if i%2 == 1 {
			out.WriteString(r.style(theme.Accent, "'"+part+"'"))
			continue
		}
		out.WriteString(r.style(base, part))
	}
	return out.String()
}

// Caption prints the muted "·"-joined context line above a listing, skipping
// empty parts.
func (r *Renderer) Caption(parts ...string) {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	r.line(r.style(theme.Muted, strings.Join(kept, " · ")))
}

// Section prints a divider label between blocks of output.
func (r *Renderer) Section(label string) {
	r.line(r.style(theme.Muted, "── "+label+" ──"))
}

// Raw prints text verbatim, with no styling or interpretation.
func (r *Renderer) Raw(text string) { r.line(text) }

// Package-level wrappers. Commands call these rather than threading a renderer
// through every constructor, matching the module-global console in the Python
// implementation.

func Success(msg string)                    { current.Success(msg) }
func Error(msg string)                      { current.Error(msg) }
func Caption(parts ...string)               { current.Caption(parts...) }
func Section(label string)                  { current.Section(label) }
func Raw(text string)                       { current.Raw(text) }
func Table(columns []Column, rows [][]Cell) { current.Table(columns, rows) }

func IndexedTable(table index.Table, resourceType, namespace string) {
	current.IndexedTable(table, resourceType, namespace)
}

func KeyValueTable(header string, keys []string, values map[string]string) {
	current.KeyValueTable(header, keys, values)
}

func ThemeList(active string)                        { current.ThemeList(active) }
func EngineList(active string)                       { current.EngineList(active) }
func StateHistory(history state.History)             { current.StateHistory(history) }
func State(entry state.State)                        { current.State(entry) }
func SwitchTargets(history state.History, live Live) { current.SwitchTargets(history, live) }

// pipeWidth is the width used off-terminal. Wide enough that piped or
// redirected output is never truncated, matching the Python console.
const pipeWidth = 1000

// width reports the width layout that has to fit should target.
func (r *Renderer) width() int {
	file, ok := r.out.(*os.File)
	if !ok {
		return pipeWidth
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return pipeWidth
	}
	return width
}
