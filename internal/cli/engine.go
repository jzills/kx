package cli

import (
	"fmt"
	"strconv"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/spf13/cobra"
)

// EngineCommand lists the registered scan engines and persists a chosen
// default.
type EngineCommand struct {
	Config config.Loader
	Active string
}

// Execute with no argument lists the engines; with a name or an index from
// that listing it persists that engine as the default.
func (c EngineCommand) Execute(name string) error {
	if name == "" {
		render.EngineList(c.Active)
		return nil
	}
	name, err := resolveEngine(name)
	if err != nil {
		return err
	}
	if err := c.Config.SaveEngine(name); err != nil {
		return err
	}
	render.Success(fmt.Sprintf("Engine set to '%s'", name))
	return nil
}

// resolveEngine turns an argument into an engine name, accepting the index
// shown in `kx engine` as well as the name — mirrors resolveTheme.
func resolveEngine(argument string) (string, error) {
	if position, err := strconv.Atoi(argument); err == nil {
		names := scanner.Names()
		if position < 1 || position > len(names) {
			return "", fmt.Errorf(
				"Engine index %d is out of range — %d engines (run 'kx engine' to list).",
				position, len(names))
		}
		return names[position-1], nil
	}
	if !scanner.Exists(argument) {
		return "", fmt.Errorf("Unknown engine '%s'. Run 'kx engine' to list engines.", argument)
	}
	return argument, nil
}

func newEngineCommand(services Services) *cobra.Command {
	return &cobra.Command{
		Use:   "engine [name]",
		Short: "List available scan engines or persist a default choice by name or index.",
		Long: "Lists available scan engines. A name, or the row number from that listing, " +
			"persists a choice as the default kx scan uses.",
		Example: "  kx engine\n  kx engine trivy",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return EngineCommand{
				Config: config.Loader{EngineKnown: scanner.Exists},
				Active: services.Config.Engine,
			}.Execute(name)
		},
	}
}
