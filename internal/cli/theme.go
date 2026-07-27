package cli

import (
	"fmt"

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

// Execute with an empty name lists the themes; with a name it switches to it.
func (c ThemeCommand) Execute(name string) error {
	if name == "" {
		render.ThemeList(c.Active)
		return nil
	}
	if !theme.Exists(name) {
		return fmt.Errorf("Unknown theme '%s'. Run 'kx theme' to list themes.", name)
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

func newThemeCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:     "theme [name]",
		Short:   "List color themes, or switch to one",
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
