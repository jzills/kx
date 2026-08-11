package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// helpSections groups the commands on the root help screen. Definition order
// within a section is the order they appear.
//
// Listed by name rather than derived from the command tree so the grouping is a
// deliberate editorial choice, and so a command missing from here is a visible
// omission rather than silently appended.
var helpSections = []struct {
	Title    string
	Commands []string
}{
	{"Resources", []string{
		"annotate", "annotations", "context", "cp", "delete", "describe",
		"diagnostic", "edit", "events", "exec", "get", "label", "labels",
		"logs", "namespace", "port-forward", "rollout", "scale", "scan",
		"secret", "top", "tree", "yaml",
	}},
	{"History", []string{"state"}},
	{"Configuration", []string{"engine", "theme"}},
	{"Shell", []string{"completion"}},
}

// selecting is the index workflow, on the screen people reach for first.
//
// Every line here is a feature that existed only in the README: the numbering
// itself, the kind shorthand, multiple indexes, ranges, and why an -A listing
// has none. A per-command --help can't teach any of it, because none of it
// belongs to one command.
var selecting = []render.HelpItem{
	{Name: "kx get pods", Doc: "Number a listing's rows 1, 2, 3..."},
	{Name: "kx pods", Doc: "Known kinds and CRDs can drop the 'get'"},
	{Name: "kx describe 2", Doc: "Any command takes an index from that listing"},
	{Name: "kx delete 3 5", Doc: "Several indexes at once"},
	{Name: "kx delete 3..7", Doc: "A range; '..5' and '5..' leave an end open"},
	{Name: "kx get pods -A", Doc: "Every namespace, unindexed — names aren't unique"},
}

var footer = []string{
	"Run 'kx COMMAND --help' for a command's options and examples.",
	"https://github.com/jzills/kx",
}

// files names the two paths kx reads and writes. Resolved from the packages
// that own them rather than written out here, so the screen can't name a path
// kx doesn't use. A home directory kx can't locate is left out rather than
// guessed at.
func files() []render.HelpItem {
	var items []render.HelpItem
	if path, err := config.File(); err == nil {
		items = append(items, render.HelpItem{
			Name: homeRelative(path), Doc: "Settings; the Environment keys below override it",
		})
	}
	if path, err := state.File(); err == nil {
		items = append(items, render.HelpItem{
			Name: homeRelative(path), Doc: "Saved listings, navigated with kx state",
		})
	}
	return items
}

// homeRelative abbreviates a path under the home directory to "~/...", which is
// both shorter than the absolute path and what a reader can paste back into a
// shell. Anything outside the home directory is left alone.
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	rest, found := strings.CutPrefix(path, home+string(filepath.Separator))
	if !found {
		return path
	}
	return "~" + string(filepath.Separator) + rest
}

// environment lists the config overrides, plus the one convention kx honors
// that isn't its own.
func environment() []render.HelpItem {
	var items []render.HelpItem
	for _, setting := range config.Settings() {
		items = append(items, render.HelpItem{Name: setting.Env, Doc: setting.Doc})
	}
	return append(items, render.HelpItem{
		Name: "NO_COLOR", Doc: "Disable styled output, per no-color.org",
	})
}

// rootOptions renders the root command's own flags, plus the help flag cobra
// adds during execution and so isn't registered yet when help is built.
func rootOptions(root *cobra.Command) []render.HelpItem {
	var options []render.HelpItem
	// LocalFlags rather than Flags: it merges the persistent set in, so
	// --no-color is listed exactly once whether or not anything has parsed
	// flags yet.
	root.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Hidden {
			return
		}
		options = append(options, render.HelpItem{Name: flagNames(flag), Doc: flag.Usage})
	})
	return append(options, render.HelpItem{
		Name: "-h, --help", Doc: "Show this message and exit",
	})
}

// flagNames spells a flag the way both help screens do.
func flagNames(flag *pflag.Flag) string {
	if flag.Shorthand == "" {
		return "--" + flag.Name
	}
	return "-" + flag.Shorthand + ", --" + flag.Name
}

// installHelp replaces cobra's help output with the themed help screens, for
// the root command and every subcommand.
func installHelp(root *cobra.Command, version string) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd == root {
			render.ShowRootHelp(render.RootHelp{
				Selecting:   selecting,
				Sections:    rootSections(root),
				Options:     rootOptions(root),
				Files:       files(),
				Environment: environment(),
				Footer:      footer,
				Version:     version,
			})
			return
		}
		render.ShowCommandHelp(commandHelp(cmd))
	})
	// Cobra prints usage on its own for an unknown command; the error already
	// says what went wrong.
	root.SetUsageFunc(func(*cobra.Command) error { return nil })
}

func rootSections(root *cobra.Command) []render.HelpSection {
	byName := map[string]*cobra.Command{}
	for _, cmd := range root.Commands() {
		byName[cmd.Name()] = cmd
	}

	sections := make([]render.HelpSection, 0, len(helpSections))
	for _, section := range helpSections {
		items := make([]render.HelpItem, 0, len(section.Commands))
		for _, name := range section.Commands {
			cmd, ok := byName[name]
			if !ok {
				continue
			}
			items = append(items, render.HelpItem{Name: name, Doc: cmd.Short})
		}
		if len(items) > 0 {
			sections = append(sections, render.HelpSection{Title: section.Title, Items: items})
		}
	}
	return sections
}

func commandHelp(cmd *cobra.Command) render.CommandHelp {
	spec := ParseUse(cmd.Use)

	doc := cmd.Long
	if doc == "" {
		doc = cmd.Short
	}
	if spec.Passthrough != "" {
		doc += "\n\nUnrecognized flags are passed through to kubectl."
	}

	// Use carries the argument spec after the command name; the name itself is
	// already in the path.
	usage := cmd.CommandPath()
	if fields := strings.Fields(cmd.Use); len(fields) > 1 {
		usage += " [OPTIONS] " + strings.Join(fields[1:], " ")
	} else {
		usage += " [OPTIONS]"
	}

	var subcommands []render.HelpItem
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Name() == "help" {
			continue
		}
		subcommands = append(subcommands, render.HelpItem{Name: child.Name(), Doc: child.Short})
	}
	sort.Slice(subcommands, func(i, j int) bool { return subcommands[i].Name < subcommands[j].Name })

	var options []render.HelpItem
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Hidden {
			return
		}
		names := "--" + flag.Name
		if flag.Shorthand != "" {
			names += "  -" + flag.Shorthand
		}
		options = append(options, render.HelpItem{Name: names, Doc: flag.Usage})
	})

	var examples []string
	for _, line := range strings.Split(cmd.Example, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			examples = append(examples, trimmed)
		}
	}

	return render.CommandHelp{
		Path:     cmd.CommandPath(),
		Doc:      doc,
		Usage:    usage,
		Commands: subcommands,
		Args:     positionalArgs(cmd.Use),
		Options:  options,
		Aliases:  cmd.Aliases,
		Examples: examples,
	}
}

// argSpec matches one positional-argument group in a Use string: <required> or
// [optional], with any trailing ellipsis.
var argSpec = regexp.MustCompile(`([<\[])([^<>\[\]]+)[>\]](\.{3})?`)

// Arg is one positional argument declared in a command's Use string.
type Arg struct {
	Name     string
	Required bool
	Variadic bool
}

// UseSpec is everything a command's Use string declares about what follows the
// command name.
type UseSpec struct {
	Args []Arg
	// Passthrough is the flag placeholder's text ("kubectl flags", "scanner
	// flags"), or "" for a command that forwards nothing.
	Passthrough string
}

// ParseUse reads a command's argument spec out of its Use string, where <name>
// is required and [name] optional. Cobra has no argument objects to introspect,
// so the spec that documents the command is also what describes it.
//
// Groups are matched whole rather than split on whitespace, so the brackets and
// any trailing ellipsis stay out of the name — splitting on spaces turns
// "[index]..." into the argument "index]..." and "[scanner flags]" into an
// argument named "scanner". A group ending in "flags" documents flag
// pass-through and names nothing the user supplies; any other multi-word group
// is named by its last word, which is what "[-- command]" means.
func ParseUse(use string) UseSpec {
	var spec UseSpec
	for _, match := range argSpec.FindAllStringSubmatch(use, -1) {
		open, body, ellipsis := match[1], strings.TrimSpace(match[2]), match[3]
		if strings.HasSuffix(body, "flags") {
			spec.Passthrough = body
			continue
		}
		fields := strings.Fields(body)
		if len(fields) == 0 {
			continue
		}
		name := strings.TrimRight(fields[len(fields)-1], ".")
		spec.Args = append(spec.Args, Arg{
			Name:     name,
			Required: open == "<",
			// The ellipsis sits either inside the brackets ("[key=value...]")
			// or after them ("<index>..."); both mean repeatable.
			Variadic: ellipsis != "" || strings.HasSuffix(fields[len(fields)-1], "..."),
		})
	}
	return spec
}

func positionalArgs(use string) []render.HelpItem {
	var args []render.HelpItem
	for _, arg := range ParseUse(use).Args {
		doc := "optional"
		if arg.Required {
			doc = "required"
		}
		args = append(args, render.HelpItem{Name: arg.Name, Doc: doc})
	}
	return args
}

// CommandOrder returns the command names in the order the root help screen
// lists them, which is the order the README's command table uses too.
func CommandOrder() []string {
	var names []string
	for _, section := range helpSections {
		names = append(names, section.Commands...)
	}
	return names
}

// Execute runs the command tree, resolving a bare kind spelling to `kx get`.
//
// `kx pods` means `kx get pods`. Registered commands always win, so `kx ns 3`
// keeps its namespace-switch meaning rather than listing namespaces — only a
// spelling that matches no command reaches the alias.
func Execute(root *cobra.Command, args []string) error {
	root.SetArgs(rewriteKindAlias(root, args))
	return root.Execute()
}

func rewriteKindAlias(root *cobra.Command, args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	if cmd, _, err := root.Find(args[:1]); err == nil && cmd != root {
		return args
	}
	if !kinds.IsKindSpelling(args[0]) {
		return args
	}
	return append([]string{"get"}, args...)
}
