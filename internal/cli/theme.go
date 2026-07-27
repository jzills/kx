package cli

import (
	"fmt"
	"strconv"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/theme"
	"github.com/spf13/cobra"
)

// ThemeCommand lists the available palettes and persists a chosen one.
type ThemeCommand struct {
	Config config.Loader
	Active string
}

// Execute with no argument lists the themes; with a name or an index from that
// listing it switches to that theme.
func (c ThemeCommand) Execute(name string) error {
	if name == "" {
		render.ThemeList(c.Active)
		return nil
	}
	name, err := resolveTheme(name)
	if err != nil {
		return err
	}
	if err := c.Config.SaveTheme(name); err != nil {
		return err
	}
	// Re-render the list in the newly chosen theme, so the effect of the switch
	// is visible immediately rather than on the next command.
	render.Configure(name, false)
	render.Success(fmt.Sprintf("Theme set to '%s'", name))
	return nil
}

// resolveTheme turns an argument into a theme name, accepting the index shown
// in `kx theme` as well as the name — the listing numbers its rows, so typing
// the number is the obvious thing to try.
func resolveTheme(argument string) (string, error) {
	if position, err := strconv.Atoi(argument); err == nil {
		names := theme.Names()
		if position < 1 || position > len(names) {
			return "", fmt.Errorf(
				"Theme index %d is out of range — %d themes (run 'kx theme' to list).",
				position, len(names))
		}
		return names[position-1], nil
	}
	if !theme.Exists(argument) {
		return "", fmt.Errorf("Unknown theme '%s'. Run 'kx theme' to list themes.", argument)
	}
	return argument, nil
}

func newThemeCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:     "theme [name]",
		Short:   "List available color themes or persist a choice by name or index.",
		Example: "  kx theme\n  kx theme dracula",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return ThemeCommand{
				Config: config.Loader{ThemeKnown: theme.Exists},
				Active: services.Config.Theme,
			}.Execute(name)
		},
	}
}
