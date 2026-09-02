package render

import (
	"strings"

	"github.com/jzills/kx/internal/mark"
	"github.com/jzills/kx/internal/theme"
)

// HelpItem is one named entry in a help listing: a command, argument or flag,
// with its description.
type HelpItem struct {
	Name string
	Doc  string
}

// HelpSection groups commands on the root help screen.
type HelpSection struct {
	Title string
	Items []HelpItem
}

// gutter is the space between a help screen's columns, and its left margin.
const gutter = "  "

// pad right-aligns a help column so descriptions line up.
func padName(name string, width int) string {
	if len(name) >= width {
		return name
	}
	return name + strings.Repeat(" ", width-len(name))
}

// Detail renders aligned label/value lines, which is the shape `kx --version`
// prints its build detail in.
//
// Returns a string rather than writing, because cobra prints the version
// through a template it owns rather than through the renderer. The styling
// still lives here: every style kx applies is named by meaning in this package,
// and a caller assembling escape codes itself would be the one place that
// wasn't true.
//
// The value carries the prominent style and the label the muted one — the
// inverse of itemBlock, whose Name leads and whose Doc explains. Here the label
// is the question ("commit") and the value is the answer.
func (r *Renderer) Detail(pairs [][2]string) string {
	width := 0
	for _, pair := range pairs {
		if len(pair[0]) > width {
			width = len(pair[0])
		}
	}
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		lines = append(lines, gutter+
			r.style(theme.Muted, padName(pair[0], width))+gutter+
			r.style(theme.Body, pair[1]))
	}
	return strings.Join(lines, "\n")
}

// Detail renders label/value lines through the package-level renderer.
func Detail(pairs [][2]string) string { return current.Detail(pairs) }

// helpWidth is the width help text wraps to.
//
// Capped rather than taken straight from the terminal: prose set to the full
// width of a maximized window is measurably harder to read, and the help
// screens are mostly prose. Narrow terminals still get their own width, and
// off-terminal output keeps pipeWidth so redirected help is never re-wrapped
// to something a reader didn't ask for.
const helpMaxWidth = 92

func (r *Renderer) helpWidth() int {
	width := r.width()
	if width > helpMaxWidth {
		return helpMaxWidth
	}
	return width
}

// wrapText breaks text into lines that fit within width, preserving the blank
// lines between paragraphs. Words longer than the width (a URL, a long path)
// are left whole rather than broken, since a split one can't be clicked or
// copied.
func wrapText(text string, width int) []string {
	if width < 20 {
		width = 20
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		fields := strings.Fields(paragraph)
		if len(fields) == 0 {
			lines = append(lines, "")
			continue
		}
		current := fields[0]
		for _, word := range fields[1:] {
			if len(current)+1+len(word) > width {
				lines = append(lines, current)
				current = word
				continue
			}
			current += " " + word
		}
		lines = append(lines, current)
	}
	return lines
}

// minDocWidth is the narrowest the description column is allowed to get.
//
// The name column grows to fit its longest name, which is right while names
// are identifiers kx chose — a flag, a command, a KX_* variable. The Files
// block's two names are paths the caller chose, and KX_CONFIG/KX_STATE can
// point anywhere, so one long path used to widen the column past the whole
// screen and push every description off the right edge. A name wider than the
// budget now takes a line of its own instead of moving everyone's column.
const minDocWidth = 24

// itemBlock renders a titled name/description list, wrapping each description
// into the column the names leave behind so a long one stacks under itself
// rather than running off the terminal.
//
// The name column is the caller's minimum, widened to fit this block's own
// names: the blocks on a screen line up with each other where they can, and a
// block whose names overflow moves its own descriptions across together.
func (r *Renderer) itemBlock(title string, items []HelpItem, minWidth int) {
	if len(items) == 0 {
		return
	}
	width := r.helpWidth()
	// The widest name column this screen can afford before names start costing
	// descriptions their room. Never narrower than the caller's minimum, which
	// is a deliberate constant (rootNameWidth, commandNameWidth) and not
	// something a cramped terminal should be allowed to undercut — that would
	// change the layout for every narrow terminal to fix a case none of them
	// have.
	budget := width - 2*len(gutter) - minDocWidth
	if budget < minWidth {
		budget = minWidth
	}
	nameWidth := minWidth
	for _, item := range items {
		if len(item.Name) > nameWidth && len(item.Name) <= budget {
			nameWidth = len(item.Name)
		}
	}

	indent := len(gutter) + nameWidth + len(gutter)
	docWidth := width - indent

	r.Blank()
	// An empty title is a block that continues the one above it — the examples
	// under the Usage heading — rather than a heading rendered blank.
	if title != "" {
		r.line(r.style(theme.Header, title))
	}
	for _, item := range items {
		wrapped := wrapText(item.Doc, docWidth)
		// A name too wide for the column takes a line of its own, and its
		// description follows in the column the rest of the block uses. The
		// name is printed whole: wrapText already leaves a long word unbroken
		// because a split one can't be pasted, and a path is that case.
		if len(item.Name) > nameWidth {
			r.line(gutter + r.style(theme.Body, item.Name))
			for _, line := range wrapped {
				r.line(strings.Repeat(" ", indent) + r.style(theme.Muted, line))
			}
			continue
		}
		name := gutter + r.style(theme.Body, padName(item.Name, nameWidth)) + gutter
		if len(wrapped) == 0 {
			r.line(strings.TrimRight(name, " "))
			continue
		}
		r.line(name + r.style(theme.Muted, wrapped[0]))
		for _, continuation := range wrapped[1:] {
			r.line(strings.Repeat(" ", indent) + r.style(theme.Muted, continuation))
		}
	}
}

// RootHelp is everything the top-level help screen shows.
//
// Passed in rather than built here so the screen states one set of facts: the
// options come from the flags actually registered, and the settings from the
// config package that reads them. The hardcoded option list this replaced had
// already drifted from the flag it documented.
type RootHelp struct {
	// Examples introduce the index workflow: a spelling paired with what it
	// does. Sized to the same column as the sections below it, and printed
	// under the same Usage heading as the argv line, since both answer "how do
	// I spell this".
	Examples []HelpItem
	Sections []HelpSection
	Options  []HelpItem
	Files    []HelpItem
	// Environment lists the variables that override the config file.
	Environment []HelpItem
	// Footer closes the screen — where to go next, in plain sentences.
	Footer []string
	// Version signs the screen off, already spelled the way it should read.
	// The renderer prints it verbatim: whether it carries a "v", and how much
	// of an untagged build's version survives, is the caller's call.
	Version string
}

// rootNameWidth is the name column shared by every block on the root screen.
// Wide enough for the longest example spelling and for KX_MAX_HISTORY, so the
// descriptions line up down the whole page rather than per section.
const rootNameWidth = 18

// RootHelp renders the top-level help screen.
func (r *Renderer) RootHelp(help RootHelp) {
	r.Blank()
	for _, line := range mark.Lines {
		r.line(r.style(theme.Header, line))
	}
	r.line(r.style(theme.Muted, "kubectl, indexed."))
	// The argv line and the examples share one heading. Titling the examples
	// separately put the word "Usage" on the screen twice, three lines apart,
	// naming the same thing both times.
	r.Blank()
	r.line(r.style(theme.Header, "Usage"))
	r.line(gutter + r.style(theme.Body, "kx [OPTIONS] COMMAND [ARGS]..."))
	r.itemBlock("", help.Examples, rootNameWidth)

	for _, section := range help.Sections {
		// The command list is the one block that truncates rather than wraps:
		// a one-line-per-command index stays scannable, and the full text is
		// one `kx COMMAND --help` away.
		items := make([]HelpItem, 0, len(section.Items))
		for _, item := range section.Items {
			items = append(items, HelpItem{Name: item.Name, Doc: shortHelp(item.Doc, shortHelpLimit)})
		}
		r.itemBlock(section.Title, items, rootNameWidth)
	}

	r.itemBlock("Options", help.Options, rootNameWidth)
	r.itemBlock("Files", help.Files, rootNameWidth)
	r.itemBlock("Environment", help.Environment, rootNameWidth)

	if len(help.Footer) > 0 {
		r.Blank()
		for _, line := range help.Footer {
			r.line(r.style(theme.Muted, line))
		}
	}

	if help.Version != "" {
		r.Blank()
		r.line(r.style(theme.Muted, help.Version))
	}
}

// commandNameWidth is the name column on a command screen. Wider than the root
// screen's because a flag carries its shorthand and its value type
// ("-n, --namespace string") where a command carries only its name.
const commandNameWidth = 22

// CommandHelp renders the help screen for a single command.
type CommandHelp struct {
	Path     string
	Doc      string
	Usage    string
	Commands []HelpItem
	Args     []HelpItem
	Options  []HelpItem
	// Global are the flags inherited from the root command. Kept apart from
	// Options so a command's own flags read as a short list rather than being
	// padded out by ones every command has.
	Global   []HelpItem
	Aliases  []string
	Examples []string
}

func (r *Renderer) CommandHelp(help CommandHelp) {
	r.Blank()
	r.line(r.style(theme.Header, help.Path))
	if help.Doc != "" {
		for _, line := range wrapText(help.Doc, r.helpWidth()) {
			r.line(r.style(theme.Muted, line))
		}
	}
	r.Blank()
	r.line(r.style(theme.Muted, "Usage") + "  " + help.Usage)

	if len(help.Commands) > 0 {
		items := make([]HelpItem, 0, len(help.Commands))
		for _, item := range help.Commands {
			items = append(items, HelpItem{Name: item.Name, Doc: shortHelp(item.Doc, shortHelpLimit)})
		}
		r.itemBlock("Commands", items, commandNameWidth)
	}

	r.itemBlock("Arguments", help.Args, commandNameWidth)
	r.itemBlock("Options", help.Options, commandNameWidth)
	r.itemBlock("Global options", help.Global, commandNameWidth)

	if len(help.Aliases) > 0 {
		r.Blank()
		r.line(r.style(theme.Header, "Aliases"))
		for _, alias := range help.Aliases {
			r.line("  " + r.style(theme.Body, alias))
		}
	}

	if len(help.Examples) > 0 {
		r.Blank()
		r.line(r.style(theme.Header, "Examples"))
		for _, example := range help.Examples {
			r.line("  " + r.style(theme.Muted, "$") + " " + example)
		}
	}
}

// ShowRootHelp and ShowCommandHelp render through the package-level renderer.
func ShowRootHelp(help RootHelp)       { current.RootHelp(help) }
func ShowCommandHelp(help CommandHelp) { current.CommandHelp(help) }
