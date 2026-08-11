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

	r.rootBlock("Selecting resources", help.Selecting)

	for _, section := range help.Sections {
		r.Blank()
		r.line(r.style(theme.Header, section.Title))
		for _, item := range section.Items {
			r.line("  " + r.style(theme.Body, padName(item.Name, rootNameWidth)) + "  " +
				r.style(theme.Muted, shortHelp(item.Doc, shortHelpLimit)))
		}
	}

	r.rootBlock("Options", help.Options)
	r.rootBlock("Files", help.Files)
	r.rootBlock("Environment", help.Environment)

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

// rootBlock renders one titled name/description block, or nothing when it is
// empty — the sections below the command list are all optional.
//
// The column is the page's, widened if this block's own names overflow it, so
// a long name pushes its block's descriptions across together instead of
// leaving one row jutting out of the alignment.
func (r *Renderer) rootBlock(title string, items []HelpItem) {
	if len(items) == 0 {
		return
	}
	width := rootNameWidth
	for _, item := range items {
		if len(item.Name) > width {
			width = len(item.Name)
		}
	}
	r.Blank()
	r.line(r.style(theme.Header, title))
	for _, item := range items {
		r.line("  " + r.style(theme.Body, padName(item.Name, width)) + "  " +
			r.style(theme.Muted, item.Doc))
	}
}

// CommandHelp renders the help screen for a single command.
type CommandHelp struct {
	Path     string
	Doc      string
	Usage    string
	Commands []HelpItem
	Args     []HelpItem
	Options  []HelpItem
	Aliases  []string
	Examples []string
}

func (r *Renderer) CommandHelp(help CommandHelp) {
	r.Blank()
	r.line(r.style(theme.Header, help.Path))
	if help.Doc != "" {
		for _, line := range strings.Split(help.Doc, "\n") {
			r.line(r.style(theme.Muted, line))
		}
	}
	r.Blank()
	r.line(r.style(theme.Muted, "Usage") + "  " + help.Usage)

	if len(help.Commands) > 0 {
		r.Blank()
		r.line(r.style(theme.Header, "Commands"))
		for _, item := range help.Commands {
			r.line("  " + r.style(theme.Body, padName(item.Name, 14)) + "  " +
				r.style(theme.Muted, shortHelp(item.Doc, shortHelpLimit)))
		}
	}

	if len(help.Args) > 0 {
		r.Blank()
		r.line(r.style(theme.Header, "Arguments"))
		for _, arg := range help.Args {
			r.line("  " + r.style(theme.Body, padName(arg.Name, 20)) + "  " +
				r.style(theme.Muted, arg.Doc))
		}
	}

	r.Blank()
	r.line(r.style(theme.Header, "Options"))
	for _, option := range help.Options {
		r.line("  " + r.style(theme.Body, padName(option.Name, 20)) + "  " +
			r.style(theme.Muted, option.Doc))
	}
	r.line("  " + r.style(theme.Body, padName("-h, --help", 20)) + "  " +
		r.style(theme.Muted, "Show this message and exit"))

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
