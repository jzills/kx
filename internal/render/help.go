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

// RootHelp renders the top-level help screen.
func (r *Renderer) RootHelp(sections []HelpSection, version string) {
	r.Blank()
	for _, line := range kxArt {
		r.line(r.style(theme.Header, line))
	}
	r.line(r.style(theme.Muted, "kubectl, indexed."))
	r.Blank()
	r.line(r.style(theme.Muted, "Usage") + "  kx [OPTIONS] COMMAND [ARGS]...")

	for _, section := range sections {
		r.Blank()
		r.line(r.style(theme.Header, section.Title))
		for _, item := range section.Items {
			r.line("  " + r.style(theme.Body, padName(item.Name, 14)) + "  " +
				r.style(theme.Muted, shortHelp(item.Doc, shortHelpLimit)))
		}
	}

	r.Blank()
	r.line(r.style(theme.Header, "Options"))
	for _, option := range []HelpItem{
		{"--no-color", "Disable styled output."},
		{"-v, --version", "Show the kx version and exit."},
		{"-h, --help", "Show this message and exit."},
	} {
		r.line("  " + r.style(theme.Body, padName(option.Name, 14)) + "  " +
			r.style(theme.Muted, option.Doc))
	}

	if version != "" {
		r.Blank()
		r.line(r.style(theme.Muted, "v"+version))
	}
}

// CommandHelp renders the help screen for a single command.
type CommandHelp struct {
	Path     string
	Doc      string
	Usage    string
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
		r.style(theme.Muted, "Show this message and exit."))

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

// RootHelp and CommandHelp render through the package-level renderer.
func RootHelp(sections []HelpSection, version string) { current.RootHelp(sections, version) }
func ShowCommandHelp(help CommandHelp)                { current.CommandHelp(help) }
