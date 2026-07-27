package cli

import (
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
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
		"get", "top", "describe", "events", "logs", "labels", "annotations",
		"label", "annotate", "yaml", "delete", "edit", "exec", "tree", "rollout",
		"scale", "scan", "port-forward", "diagnostic", "namespace", "context",
	}},
	{"History", []string{"state", "drop", "back", "forward"}},
	{"Configuration", []string{"theme"}},
}

// installHelp replaces cobra's help output with the themed help screens, for
// the root command and every subcommand.
func installHelp(root *cobra.Command, version string) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd == root {
			render.RootHelp(rootSections(root), version)
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
	doc := cmd.Long
	if doc == "" {
		doc = cmd.Short
	}

	// Use carries the argument spec after the command name; the name itself is
	// already in the path.
	usage := cmd.CommandPath()
	if fields := strings.Fields(cmd.Use); len(fields) > 1 {
		usage += " [OPTIONS] " + strings.Join(fields[1:], " ")
	} else {
		usage += " [OPTIONS]"
	}

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
		Args:     positionalArgs(cmd.Use),
		Options:  options,
		Aliases:  cmd.Aliases,
		Examples: examples,
	}
}

// positionalArgs reads a command's positional arguments out of its Use string,
// where <name> is required and [name] optional. Cobra has no argument objects
// to introspect, so the spec that documents the command is also what describes
// it.
func positionalArgs(use string) []render.HelpItem {
	var args []render.HelpItem
	for _, token := range strings.Fields(use)[1:] {
		switch {
		case strings.HasPrefix(token, "<"):
			args = append(args, render.HelpItem{
				Name: strings.Trim(token, "<>."), Doc: "required"})
		case strings.HasPrefix(token, "["):
			// Flag pass-through placeholders aren't arguments the user names.
			name := strings.Trim(token, "[]")
			if strings.Contains(name, " ") || name == "kubectl" {
				continue
			}
			args = append(args, render.HelpItem{Name: name, Doc: "optional"})
		}
	}
	return args
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
