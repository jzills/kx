package render

import (
	"strings"

	"github.com/jzills/kx/internal/theme"
)

var kxArt = []string{
	"██╗  ██╗██╗  ██╗",
	"██║ ██╔╝╚██╗██╔╝",
	"█████╔╝  ╚███╔╝ ",
	"██╔═██╗  ██╔██╗ ",
	"██║  ██╗██╔╝ ██╗",
	"╚═╝  ╚═╝╚═╝  ╚═╝",
}

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

// pad right-aligns a help column so descriptions line up.
func padName(name string, width int) string {
	if len(name) >= width {
		return name
	}
	return name + strings.Repeat(" ", width-len(name))
}

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
	nameWidth := minWidth
	for _, item := range items {
		if len(item.Name) > nameWidth {
			nameWidth = len(item.Name)
		}
	}

	const gutter = "  "
	indent := len(gutter) + nameWidth + len(gutter)
	docWidth := r.helpWidth() - indent

	r.Blank()
	r.line(r.style(theme.Header, title))
	for _, item := range items {
		name := gutter + r.style(theme.Body, padName(item.Name, nameWidth)) + gutter
		wrapped := wrapText(item.Doc, docWidth)
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
	// Selecting introduces the index workflow: a spelling paired with what it
	// does. Sized to the same column as the sections below it.
	Selecting []HelpItem
	Sections  []HelpSection
	Options   []HelpItem
	Files     []HelpItem
	// Environment lists the variables that override the config file.
	Environment []HelpItem
	// Footer closes the screen — where to go next, in plain sentences.
	Footer  []string
	Version string
}

// rootNameWidth is the name column shared by every block on the root screen.
// Wide enough for the longest example spelling and for KX_MAX_HISTORY, so the
// descriptions line up down the whole page rather than per section.
const rootNameWidth = 18

// RootHelp renders the top-level help screen.
func (r *Renderer) RootHelp(help RootHelp) {
	r.Blank()
	for _, line := range kxArt {
		r.line(r.style(theme.Header, line))
	}
	r.line(r.style(theme.Muted, "kubectl, indexed."))
	r.Blank()
	r.line(r.style(theme.Muted, "Usage") + "  kx [OPTIONS] COMMAND [ARGS]...")

	r.itemBlock("Selecting resources", help.Selecting, rootNameWidth)

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
		r.line(r.style(theme.Muted, "v"+help.Version))
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
