// Package cli defines the kx commands and wires them to their services.
package cli

import (
	"fmt"
	"sync"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/discovery"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

// Services holds the dependencies commands are constructed with, so tests can
// substitute fakes for all of them.
type Services struct {
	Kubectl kubectl.Service
	State   *state.Service
	Index   Indexer
	Config  config.Config
	// Kubernetes builds the API client, for the commands that need structured
	// data kubectl's table output can't provide. Deferred rather than built up
	// front so commands that never touch the API server don't pay for loading
	// and validating a kubeconfig.
	Kubernetes func() (kubernetes.Interface, error)
	// Confirm asks for consent, returning an error to abort. Injected rather
	// than called through the renderer so a test can answer the prompt; the
	// renderer's own Confirm reads os.Stdin, which under `go test` is closed
	// and therefore always declines.
	Confirm func(string) error
}

// confirm is the consent prompt, falling back to the renderer's when a caller
// left it unset — Services is constructed literally in several tests.
func (s Services) confirm() func(string) error {
	if s.Confirm != nil {
		return s.Confirm
	}
	return render.Confirm
}

// NewServices builds the production service set from the loaded config.
func NewServices(cfg config.Config) Services {
	return Services{
		Kubectl:    kubectl.New(),
		State:      state.NewService(cfg.MaxHistory),
		Index:      index.Service{},
		Config:     cfg,
		Kubernetes: kubernetesClient,
		Confirm:    render.Confirm,
	}
}

// kubernetesClient builds the API client once per process and reuses it.
func kubernetesClient() (kubernetes.Interface, error) {
	clientOnce.Do(func() { client, clientErr = k8s.Client() })
	return client, clientErr
}

var (
	clientOnce sync.Once
	client     *kubernetes.Clientset
	clientErr  error
)

// NewRoot builds the kx command tree.
func NewRoot(services Services, version string) *cobra.Command {
	kinds.SetShorthandSource(discovery.NewSource())

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

	// `get` is the only command that doesn't consume an index, so a NotFound
	// from it means the resource type doesn't exist — refreshing the listing
	// would be beside the point. Every other command resolves an index, where a
	// NotFound usually means the saved listing has gone stale.
	installHelp(root, version)

	root.AddCommand(withoutRefresh(newGetCommand(services)))
	root.AddCommand(withoutRefresh(newEngineCommand(services)))
	root.AddCommand(withoutRefresh(newThemeCommand(services)))
	root.AddCommand(withoutRefresh(newTopCommand(services)))

	stateCmd := newStateCommand(services)
	stateCmd.AddCommand(
		newNavigateCommand(services, "back", "Navigate to the previous kx get result.", -1),
		newNavigateCommand(services, "forward", "Navigate to the next kx get result.", +1),
		newDropCommand(services),
	)
	root.AddCommand(withoutRefresh(stateCmd))

	// kx back/forward/drop predate kx state gaining subcommands. They stay
	// registered and fully working — just hidden from --help and the README
	// table — so existing scripts and muscle memory don't break.
	for _, cmd := range []*cobra.Command{
		newNavigateCommand(services, "back", "Navigate to the previous kx get result.", -1),
		newNavigateCommand(services, "forward", "Navigate to the next kx get result.", +1),
		newDropCommand(services),
	} {
		cmd.Hidden = true
		root.AddCommand(withoutRefresh(cmd))
	}

	for _, cmd := range []*cobra.Command{
		newDescribeCommand(services),
		newLogsCommand(services),
		newEditCommand(services),
		newExecCommand(services),
		newDeleteCommand(services),
		newScaleCommand(services),
		newRolloutCommand(services),
		newPortForwardCommand(services),
		newCopyCommand(services),
		newYamlCommand(services),
		newTreeCommand(services),
		newScanCommand(services),
		newSecretCommand(services, "secret", []string{"secrets"}),
		newEventsCommand(services),
		newDiagnosticCommand(services, "diagnostic", []string{"diag"}),
		newMetadataReadCommand(services, "labels", "Show labels for one or more indexed resources; --selector formats output as a label selector.", "labels", "LABEL", true),
		newMetadataReadCommand(services, "annotations", "Show annotations for one or more indexed resources.", "annotations", "ANNOTATION", false),
		newMetadataWriteCommand(services, "label", "labels", "Set or remove labels on an indexed resource."),
		newMetadataWriteCommand(services, "annotate", "annotations", "Set or remove annotations on an indexed resource."),
		newSwitchCommand(services, "namespace", "ns", "List namespaces, or switch to an indexed one; alias: kx ns.", false),
		newSwitchCommand(services, "context", "contexts", "List kubeconfig contexts, or switch to an indexed one; alias: kx contexts.", true),
	} {
		root.AddCommand(withRefresh(services, cmd))
	}
	return root
}

func newGetCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		// The indexes are documented here because runGet accepts them —
		// `kx get pods 1 3` re-fetches those two — and neither the help screen
		// nor the README table mentioned it.
		Use:   "get <resource> [index]... [kubectl flags]",
		Short: "List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3).",
		Long: "Fetches resources with kubectl and assigns each row an index.\n\n" +
			"`-n <namespace>`, label selectors and output flags all work as usual.",
		Example: "  kx get pods\n  kx get pods -n prod -l app=web\n  kx get deploy -m api",
		Args:    cobra.MinimumNArgs(1),
		// Everything after `get` belongs to kubectl unless it is one of kx's
		// own flags, which are removed by hand below. See passthrough.go for
		// why cobra can't do this.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rest, handled, err := passthrough(cmd, args, nil)
			if err != nil || handled {
				return err
			}
			rest, options, err := secretFlags(rest)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return fmt.Errorf("get requires a resource type, e.g. 'kx get pods'")
			}
			// The resource type leads; everything after is indexes or kubectl's.
			return runGet(services, rest[0], rest[1:], options)
		},
	}
	cmd.Flags().StringP("match", "m", "", "Match by name (substring, case-insensitive)")
	cmd.Flags().Bool("decode", false,
		"Show Secret data in plaintext; every Secret in the namespace when no index is given")
	cmd.Flags().StringP("key", "k", "", "With --decode, print only this key's value")
	cmd.Flags().BoolP("yes", "y", false,
		"Skip the confirmation prompt for a namespace-wide --decode")
	// Pure kubectl passthrough, parsed by hand like every other flag here —
	// registered only so they appear in --help instead of vanishing.
	cmd.Flags().StringP("namespace", "n", "", "Namespace to list from; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false, "List across every namespace; results are not indexed")
	return cmd
}

func extractNamespaceFor(services Services, extraArgs []string) string {
	if namespace := extractNamespace(extraArgs); namespace != "" {
		return namespace
	}
	return services.Kubectl.CurrentNamespace()
}
