package cli

import (
	"fmt"
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
		"annotate", "annotations", "context", "cordon", "cp", "debug", "delete",
		"describe", "diagnostic", "drain", "edit", "events", "exec", "get",
		"label", "labels", "logs", "namespace", "port-forward", "rollout",
		"scale", "scan", "secret", "top", "tree", "uncordon", "yaml",
	}},
	{"History", []string{"state"}},
	{"Configuration", []string{"engine", "theme"}},
	{"Shell", []string{"completion"}},
}

// examples are the index workflow, on the screen people reach for first.
//
// Every line here is a feature that existed only in the README: the numbering
// itself, the kind shorthand, multiple indexes, ranges, and why an -A listing
// has none. A per-command --help can't teach any of it, because none of it
// belongs to one command.
var examples = []render.HelpItem{
	{Name: "kx get pods", Doc: "Number a listing's rows 1, 2, 3..."},
	{Name: "kx pods", Doc: "Known kinds and CRDs can drop the 'get'"},
	{Name: "kx describe 2", Doc: "Any command takes an index from that listing"},
	{Name: "kx delete 3 5", Doc: "Several indexes at once"},
	{Name: "kx delete 3..7", Doc: "A range; '..5' and '5..' leave an end open"},
	{Name: "kx get pods -A", Doc: "Every namespace; indexes carry their namespace"},
}

// The docs URL used to close this screen too. It is one line under `kx
// --version`, next to the commit and the config path a reader who wants the
// project page is already looking for — repeating it here bought nothing.
var footer = []string{
	"Run 'kx COMMAND --help' for a command's options and examples.",
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

// environment lists the config overrides — kx's own keys, and only those,
// alphabetically, the way every other block on this screen is ordered.
//
// NO_COLOR is still honored (see render.New), but it is a terminal-wide
// convention rather than a kx setting, and listing it beside KX_THEME and
// KX_THEME_DISABLE implied kx owned it. The README documents it where it
// belongs, with the rest of the styling behavior.
func environment() []render.HelpItem {
	var items []render.HelpItem
	for _, setting := range config.Settings() {
		items = append(items, render.HelpItem{Name: setting.Env, Doc: setting.Doc})
	}
	// KX_STATE lives beside KX_CONFIG here rather than in config.Settings():
	// it names state.File's path, and state doesn't import config (nor
	// should it, just to list one env var) — this is the one place that
	// already imports both.
	items = append(items, render.HelpItem{
		Name: "KX_STATE", Doc: "State file path, instead of ~/.kx/state.json",
	})
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
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

// flagSpelling is flagNames plus the kind of value the flag takes, so a reader
// can tell `--port` wants a number from `--full`, which wants nothing.
func flagSpelling(flag *pflag.Flag) string {
	name := flagNames(flag)
	switch flag.Value.Type() {
	case "bool":
		return name
	case "stringArray", "stringSlice":
		return name + " strings"
	default:
		return name + " " + flag.Value.Type()
	}
}

// argDocs describes each positional argument kx accepts.
//
// Keyed by the name in the Use string rather than by command, because the
// names are shared — two dozen commands take an `index`, and it means the same
// thing to all of them. A command needing something more specific overrides it
// with an "arg.<name>" annotation.
//
// TestEveryArgumentIsDocumented fails on a name that reaches here undescribed,
// so a new command can't quietly fall back to "required".
var argDocs = map[string]string{
	"index":     "Row number from the current listing; run kx state to see it",
	"resource":  "Resource type: pods, deploy, svc, a CRD, or any kubectl kind",
	"position":  "History position, as numbered by kx state --all",
	"name":      "Name, or the row number from the listing shown above",
	"action":    "status, restart, pause, resume, history, or undo",
	"replicas":  "Number of replicas to scale to",
	"port":      "Port mapping, local:remote — e.g. 8080:80, or :80 to pick a local port",
	"src":       "Source path, or <index>:<path> inside the pod",
	"dest":      "Destination path, or <index>:<path> inside the pod",
	"key=value": "Key and value to set; repeatable",
	"command":   "Command to run in the pod instead of a shell",
}

// argDoc describes one argument, preferring a command's own annotation.
func argDoc(cmd *cobra.Command, name string) string {
	if doc, ok := cmd.Annotations["arg."+name]; ok {
		return doc
	}
	return argDocs[name]
}

// installHelp replaces cobra's help output with the themed help screens, for
// the root command and every subcommand.
//
// version is taken already spelled — the screen prints it as given rather than
// composing it, so what a version looks like stays a question for the package
// that owns the version string.
func installHelp(root *cobra.Command, version string) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd == root {
			render.ShowRootHelp(render.RootHelp{
				Examples:    examples,
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

	// Trimmed because cobra writes its own Long strings (the completion
	// command's) with a trailing newline, which rendered as a stray blank line
	// under the description.
	doc := strings.TrimSpace(cmd.Long)
	if doc == "" {
		doc = strings.TrimSpace(cmd.Short)
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

	// Split so a command's own flags aren't padded out by the ones every
	// command inherits. LocalFlags excludes a parent's persistent flags unless
	// this command shadows one, in which case its own wins — which is what a
	// reader of this screen wants either way.
	var global []render.HelpItem
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Hidden {
			return
		}
		global = append(global, render.HelpItem{Name: flagSpelling(flag), Doc: flag.Usage})
	})
	global = append(global, render.HelpItem{
		Name: "-h, --help", Doc: "Show this message and exit",
	})

	var options []render.HelpItem
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Hidden {
			return
		}
		options = append(options, render.HelpItem{Name: flagSpelling(flag), Doc: flag.Usage})
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
		Args:     positionalArgs(cmd),
		Options:  options,
		Global:   global,
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

// positionalArgs documents a command's arguments.
//
// The name column carries the Use string's own punctuation — <required>,
// [optional], a trailing ellipsis for repeatable — so the description column
// is free to say what the argument is. It used to hold the word "required" or
// "optional" and nothing else, which the Usage line above it already showed.
func positionalArgs(cmd *cobra.Command) []render.HelpItem {
	var args []render.HelpItem
	for _, arg := range ParseUse(cmd.Use).Args {
		args = append(args, render.HelpItem{
			Name: argSpelling(arg),
			Doc:  argDoc(cmd, arg.Name),
		})
	}
	return args
}

// argSpelling writes an argument the way the Use string and the README table
// do: angle brackets for required, square for optional, ellipsis for
// repeatable.
func argSpelling(arg Arg) string {
	name := "<" + arg.Name + ">"
	if !arg.Required {
		name = "[" + arg.Name + "]"
	}
	if arg.Variadic {
		name += "..."
	}
	return name
}

// minArgs builds a cobra.PositionalArgs validator that requires at least n
// positional arguments, reporting a shortfall in kx's own voice rather than
// cobra's "requires at least N arg(s), only received M" — generated from the
// command's own Use string, so it can't say something --help doesn't.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		return requiredArgsError(cmd)
	}
}

// exactArgs is minArgs' counterpart for a command whose argument count is
// fixed rather than open-ended. Both a shortfall and an overflow report the
// same message: either way, RunE did not get what the Use string promises,
// and cp/drain/port-forward's hand-written arity errors don't distinguish
// the two either.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		return requiredArgsError(cmd)
	}
}

// requiredArgsError names a command's required arguments the way its own
// Usage line already spells them, so `kx describe` with no index answers
// "kx describe requires <index>... — see 'kx describe --help' for usage."
// rather than cobra's generic arity message. Only the required prefix is
// named — an optional argument past it, like exec's trailing command, isn't
// what's missing.
func requiredArgsError(cmd *cobra.Command) error {
	var required []string
	for _, arg := range ParseUse(cmd.Use).Args {
		if !arg.Required {
			break
		}
		required = append(required, argSpelling(arg))
	}
	path := cmd.CommandPath()
	return fmt.Errorf(
		"%s requires %s — see '%s --help' for usage.",
		path, strings.Join(required, " "), path,
	)
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

// Section is one group of the root help screen: a heading and the commands
// under it.
type Section struct {
	Title    string
	Commands []string
}

// HelpSections returns the groups CommandOrder flattens.
//
// Exported for the site's command reference, which lists the same commands in
// the same groups. CommandOrder alone would have made the docs invent their own
// grouping, and a second editorial opinion about which commands belong together
// is exactly what helpSections exists to prevent.
func HelpSections() []Section {
	sections := make([]Section, 0, len(helpSections))
	for _, section := range helpSections {
		// Copied rather than aliased: helpSections is package state, and a
		// caller ranging over the slice it gets back should not be able to
		// reorder the help screen by writing to it.
		commands := append([]string(nil), section.Commands...)
		sections = append(sections, Section{Title: section.Title, Commands: commands})
	}
	return sections
}

// HelpFor returns the structured help for one command — the same data
// `kx <command> --help` renders.
//
// Exported so tools/gen-site-docs can write a documentation page from what the
// binary actually accepts, rather than from a hand-kept list of flags that
// drifts the first time one is added.
func HelpFor(cmd *cobra.Command) render.CommandHelp { return commandHelp(cmd) }

// Execute runs the command tree, resolving a bare kind spelling to `kx get`.
//
// `kx pods` means `kx get pods`. Registered commands always win, so `kx ns 3`
// keeps its namespace-switch meaning rather than listing namespaces — only a
// spelling that matches no command reaches the alias.
func Execute(root *cobra.Command, args []string) error {
	root.SetArgs(rewriteArgs(root, args))
	return root.Execute()
}

// completionRequests are cobra's own completion entry points, which carry the
// line being completed as their arguments rather than being one.
var completionRequests = map[string]bool{
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// rewriteArgs applies the kind alias to a command line, and to the line carried
// inside a completion request.
//
// `kx po <TAB>` reaches kx as `kx __complete po ""`. The alias applied to that
// outer line stops at __complete, which is a command, leaving cobra to resolve
// `po` on its own — it finds no such command and answers with
// ShellCompDirectiveDefault, the shell's *filename* completion. So the shorthand
// completed to the working directory while `kx get po <TAB>` completed to the
// listing. Rewriting the inner line is what makes the two agree, for indexes and
// flag values alike.
//
// The last word is excluded: it is the one being completed, so it is a fragment
// rather than a spelling. Rewriting `kx po<TAB>` to `kx get po` would answer
// with resource types where the shell asked which commands start with "po",
// dropping port-forward from what that offers today.
func rewriteArgs(root *cobra.Command, args []string) []string {
	if len(args) < 2 || !completionRequests[args[0]] {
		return rewriteKindAlias(root, args)
	}
	line := rewriteKindAlias(root, args[1:len(args)-1])
	rewritten := make([]string, 0, len(line)+2)
	rewritten = append(rewritten, args[0])
	rewritten = append(rewritten, line...)
	return append(rewritten, args[len(args)-1])
}

func rewriteKindAlias(root *cobra.Command, args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	if shadowedByCommand(root, args[0]) {
		return args
	}
	if !kinds.IsKindSpelling(args[0]) {
		return args
	}
	return append([]string{"get"}, args...)
}

// shadowedByCommand reports whether a word names a registered command, aliases
// included — which always wins over the kind alias. Shared with the root
// completer, so what completion offers cannot promise what running it will not
// do.
func shadowedByCommand(root *cobra.Command, word string) bool {
	cmd, _, err := root.Find([]string{word})
	return err == nil && cmd != root
}
