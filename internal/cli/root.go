// Package cli defines the kx commands and wires them to their services.
package cli

import (
	"fmt"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// Services holds the dependencies commands are constructed with, so tests can
// substitute fakes for all of them.
type Services struct {
	Kubectl kubectl.Service
	State   *state.Service
	Index   Indexer
	Config  config.Config
}

// NewServices builds the production service set from the loaded config.
func NewServices(cfg config.Config) Services {
	return Services{
		Kubectl: kubectl.New(),
		State:   state.NewService(cfg.MaxHistory),
		Index:   index.Service{},
		Config:  cfg,
	}
}

// NewRoot builds the kx command tree.
func NewRoot(services Services, version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "kx",
		Short:         "Select Kubernetes resources by index instead of typing names",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Match the Python CLI, which exposes -v as the version alias and -h for help.
	root.SetVersionTemplate("kx {{.Version}}\n")
	root.Flags().BoolP("version", "v", false, "Show the installed version")
	root.PersistentFlags().Bool("no-color", false, "Disable styled output")

	root.AddCommand(newGetCommand(services))
	root.AddCommand(newThemeCommand(services))
	return root
}

func newGetCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <resource> [kubectl flags]",
		Short: "List resources and assign each row an index",
		Long: "Fetches resources with kubectl and assigns each row an index.\n\n" +
			"Unrecognized flags are passed through to kubectl, so `-n <namespace>`,\n" +
			"label selectors and output flags all work as usual.",
		Example: "  kx get pods\n  kx get pods -n prod -l app=web\n  kx get deploy -m api",
		Args:    cobra.MinimumNArgs(1),
		// Everything after `get` belongs to kubectl unless it is one of kx's
		// own flags, which are removed by hand below. See passthrough.go for
		// why cobra can't do this.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if help, _ := extractBool(args, "-h", "--help"); help {
				return cmd.Help()
			}
			match, rest, err := extractString(args, "--match", "-m")
			if err != nil {
				return err
			}
			// kx's own global flag; kubectl would reject it.
			_, rest = extractBool(rest, "--no-color")
			if len(rest) == 0 {
				return fmt.Errorf("get requires a resource type, e.g. 'kx get pods'")
			}

			get := GetCommand{
				Kubectl: services.Kubectl,
				State:   services.State,
				Index:   services.Index,
			}
			resource, extra := rest[0], rest[1:]
			output, err := get.Execute(resource, match, extra)
			if err != nil {
				return err
			}
			render.IndexedTable(output, resource, extractNamespaceFor(services, extra), "")
			return nil
		},
	}
	cmd.Flags().StringP("match", "m", "", "Filter rows by name substring")
	return cmd
}

func extractNamespaceFor(services Services, extraArgs []string) string {
	if namespace := extractNamespace(extraArgs); namespace != "" {
		return namespace
	}
	return services.Kubectl.CurrentNamespace()
}
