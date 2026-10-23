// Package cli defines the kx commands and wires them to their services.
package cli

import (
	"strconv"
	"sync"

	"github.com/jzills/kx/internal/buildinfo"
	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
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
	// Scanner runs the vulnerability scanner. Injected for the same reason as
	// Confirm: kx scan built an ExecService inline, so every test of that
	// command stopped at the argument guards rather than reaching what the
	// command does with what it scanned.
	Scanner scanner.Service
	// KubectlClient finds the kubectl binary for `kx --version`. Injected so a
	// test of the version screen does not depend on the kubectl installed on
	// the machine running it; nil means kubectl.DetectClient.
	KubectlClient func() kubectl.Client
}

// confirm is the consent prompt, falling back to the renderer's when a caller
// left it unset — Services is constructed literally in several tests.
func (s Services) confirm() func(string) error {
	if s.Confirm != nil {
		return s.Confirm
	}
	return render.Confirm
}

// scannerService is the scanner runner, falling back to the real one for the
// same reason confirm does. Named for the field rather than the package, which
// is already spelled `scanner` here.
func (s Services) scannerService() scanner.Service {
	if s.Scanner != nil {
		return s.Scanner
	}
	return scanner.ExecService{}
}

// NewServices builds the production service set from the loaded config.
func NewServices(cfg config.Config) Services {
	client := kubectl.New()
	client.Warn = render.Warning
	states := state.NewService(cfg.MaxHistory)
	// Every entry the state service writes records the context it was listed
	// against. Wired here, as a hook rather than a value, because the state
	// service is the one thing every save path goes through — the tree walk and
	// the triage sweep both save without holding a kubectl service of their own.
	states.Context = client.CurrentContext
	return Services{
		Kubectl:    client,
		State:      states,
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
	// Resolved here so an unstamped build reports its module and VCS metadata
	// everywhere the version appears, not just under --version.
	info := buildinfo.Resolve(version)
	root := &cobra.Command{
		Use:           "kx",
		Short:         "Select Kubernetes resources by index instead of typing names.",
		Version:       info.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Match the Python CLI, which exposes -v as the version alias and -h for help.
	installVersion(root, info, services.KubectlClient)
	root.Flags().BoolP("version", "v", false, "Show the kx version and exit")
	root.PersistentFlags().Bool("no-color", false, "Disable styled output")
	root.SetFlagErrorFunc(ownFlagError)

	// `get` is the only command that doesn't consume an index, so a NotFound
	// from it means the resource type doesn't exist — refreshing the listing
	// would be beside the point. Every other command resolves an index, where a
	// NotFound usually means the saved listing has gone stale.
	installHelp(root, info.ShortTag())

	root.AddCommand(withoutRefresh(newGetCommand(services)))
	root.AddCommand(withoutRefresh(newEngineCommand(services)))
	root.AddCommand(withoutRefresh(newThemeCommand(services)))
	root.AddCommand(withoutRefresh(newTopCommand(services)))
	// withoutRefresh: the wrapper re-runs a saved query when kubectl reports a
	// resource gone, and kx ref never asks kubectl anything. It reports what
	// the index means, which is knowable with no cluster reachable at all.
	root.AddCommand(withoutRefresh(newRefCommand(services)))
	// kx mark resolves an index exactly the way kx describe does, and a stale
	// or context-mismatched index deserves the same recovery: a fresh listing
	// to mark from, rather than a dead end. kx unmark takes no index and never
	// resolves one, so it stays without the wrapper.
	root.AddCommand(withRefresh(services, newMarkCommand(services)))
	root.AddCommand(withoutRefresh(newUnmarkCommand(services)))
	// withoutRefresh: the server resolves nothing from the listing, so there
	// is no stale index for the wrapper to recover from.
	root.AddCommand(withoutRefresh(newMCPCommand(services, info.Version)))

	stateCmd := newStateCommand(services)
	stateCmd.AddCommand(
		newNavigateCommand(services, "back", "Navigate to the previous kx get result.",
			"Moves the cursor to the previous kx get result, clamped at the start of the stack.", -1),
		newNavigateCommand(services, "forward", "Navigate to the next kx get result.",
			"Moves the cursor to the next kx get result, clamped at the end of the stack.", +1),
		newDropCommand(services),
	)
	root.AddCommand(withoutRefresh(stateCmd))
	root.AddCommand(withoutRefresh(newSetCommand(services)))

	for _, cmd := range []*cobra.Command{
		newDescribeCommand(services),
		newLogsCommand(services),
		newEditCommand(services),
		newExecCommand(services),
		newDebugCommand(services),
		newCordonCommand(services, "cordon"),
		newCordonCommand(services, "uncordon"),
		newDeleteCommand(services),
		newDrainCommand(services),
		newScaleCommand(services),
		newWaitCommand(services),
		newRolloutCommand(services),
		newPortForwardCommand(services),
		newCopyCommand(services),
		newYamlCommand(services),
		newTreeCommand(services),
		newScanCommand(services),
		newSecretCommand(services, "secret", []string{"secrets"}),
		newEventsCommand(services),
		newDiagnosticCommand(services, "diagnostic", []string{"diag"}),
		newMetadataReadCommand(services, "labels",
			"Show labels for one or more indexed resources; --selector formats output as a label selector.",
			"Shows every label on one or more indexed resources, one table per resource. "+
				"--selector reformats the same data as a copy-pastable label selector.",
			"labels", "LABEL", true),
		newMetadataReadCommand(services, "annotations",
			"Show annotations for one or more indexed resources.",
			"Shows every annotation on one or more indexed resources, one table per resource.",
			"annotations", "ANNOTATION", false),
		newMetadataWriteCommand(services, "label", "labels",
			"Set or remove labels on an indexed resource.",
			"Sets or removes labels on one indexed resource — key=value to set, --remove to drop a key."),
		newMetadataWriteCommand(services, "annotate", "annotations",
			"Set or remove annotations on an indexed resource.",
			"Sets or removes annotations on one indexed resource — key=value to set, --remove to drop a key."),
		newSwitchCommand(services, "namespace", "ns", "List Namespaces, or switch to an indexed one; alias: kx ns.", false),
		newSwitchCommand(services, "context", "contexts", "List kubeconfig contexts, or switch to an indexed one; alias: kx contexts.", true),
	} {
		root.AddCommand(withRefresh(services, cmd))
	}

	installCompletion(root)
	installCompletions(root, services)
	return root
}

// installCompletion adds cobra's completion command up front and gives it kx's
// voice.
//
// Cobra otherwise creates it during Execute, which left a working command that
// no help screen could see: not the root listing, which is built from the
// command tree, and not the README table generated from the same tree. Shell
// completion was real but discoverable only by knowing cobra.
func installCompletion(root *cobra.Command) {
	root.InitDefaultCompletionCmd()
	completion, _, err := root.Find([]string{"completion"})
	if err != nil || completion == root {
		return
	}
	completion.Short = "Generate a shell completion script for kx (bash, zsh, fish, powershell)."
	completion.Long = "Generates a shell completion script for kx. See each subcommand's " +
		"own --help for how to install it."
	// Labeled by shell because the help screen prints each example on its
	// own "$ " line with nothing between them, so an unlabeled pair reads as
	// a sequence to run. Someone on zsh ran both: the bash script registers
	// its own completer over the zsh one where bashcompinit is loaded, and
	// then fails on a bash-completion helper zsh has no equivalent for.
	completion.Example = "  kx completion zsh > \"${fpath[1]}/_kx\"   # zsh, then start a new shell\n" +
		"  source <(kx completion bash)            # bash, this shell only"
}

func newGetCommand(services Services) *cobra.Command {
	cmd := &cobra.Command{
		// The indexes are documented here because runGet accepts them —
		// `kx get pods 1 3` re-fetches those two — and neither the help screen
		// nor the README table mentioned it.
		Use:        "get <resource> [index]... [kubectl flags]",
		SuggestFor: []string{"list", "ls", "ps"},
		Short:      "List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3).",
		Long: "Fetches resources with kubectl and assigns each row an index.\n\n" +
			"`-n <namespace>`, label selectors and output flags all work as usual.\n\n" +
			"A cluster-scoped kind — Nodes, PersistentVolumes, StorageClasses, a " +
			"cluster-scoped CRD — takes neither `-n` nor `-A`. There is no namespace " +
			"for either to name, so kx refuses them rather than listing something " +
			"other than what was asked for.\n\n" +
			"A `--match` term narrows the table, a watch's live table, `-o name` or a " +
			"`-o json`/`-o yaml` collection by each row's name. A template is a shape " +
			"you chose, which kx cannot assume has names in it, so it refuses the term " +
			"there rather than printing everything.",
		Example: "  kx get pods\n  kx get pods -n prod -l app=web\n  kx get deploy -m api\n  kx get pods 1..3\n  kx get pods 3..\n  kx get pods --watch\n  kx get pods --sort-by=.metadata.creationTimestamp",
		Args:    minArgs(1),
	}
	kubectlFlags := registerGetFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		positional, forward := kubectlArgv(cmd, args, kubectlFlags)
		if len(positional) == 0 {
			return requiredArgsError(cmd)
		}
		options, err := getOptionsFrom(cmd)
		if err != nil {
			return err
		}
		// Positionals first, then kubectl's flags: the indexes follow the
		// resource, and runGet reads them as the leading run of what is left.
		// What followed `--` is handed over apart, so none of it is an index.
		options.AfterDash = afterDash(cmd, args)
		flags := forward[:len(forward)-len(options.AfterDash)]
		rest := append(positional[1:len(positional):len(positional)], flags...)
		// A number here is a row, not a kind — the indexes follow the
		// resource, they do not replace it.
		if _, err := strconv.Atoi(positional[0]); err == nil {
			return numericResourceError(services, append([]string{positional[0]}, rest...))
		}
		return runGet(services, positional[0], rest, options)
	}
	return cmd
}

// registerGetFlags registers the flags kx get and kx secret share: kx's own,
// then kubectl get's from the table.
func registerGetFlags(cmd *cobra.Command) *kubectlArgs {
	cmd.Flags().StringP("match", "m", "", matchUsage)
	cmd.Flags().Bool("decode", false,
		"Show Secret data in plaintext; every Secret in the namespace when no index is given")
	cmd.Flags().StringP("key", "k", "", "With --decode, print only this key's value")
	cmd.Flags().BoolP("yes", "y", false,
		"Skip the confirmation prompt for a namespace-wide --decode")
	// kubectl's flags that kx also reads, registered first so they keep kx's
	// own usage; registerKubectlFlags gives them kubectl's shape and forwards
	// them with the rest.
	cmd.Flags().StringP("namespace", "n", "", "Namespace to list from; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false, "List across every namespace; each row is indexed and carries its own namespace")
	registerFormatFlags(cmd)
	registerWatchFlag(cmd)
	return registerKubectlFlags(cmd, "get")
}

// getOptionsFrom reads kx get's own flags, which cobra parsed.
//
// --key is read by presence, not emptiness: `-k ""` asks for a key that
// cannot exist, and must fail as a missing key rather than silently widening
// into a dump of every value in the Secret.
func getOptionsFrom(cmd *cobra.Command) (getOptions, error) {
	flags := cmd.Flags()
	var options getOptions
	var err error
	if options.Match, err = flags.GetString("match"); err != nil {
		return options, err
	}
	if options.Decode, err = flags.GetBool("decode"); err != nil {
		return options, err
	}
	if options.Yes, err = flags.GetBool("yes"); err != nil {
		return options, err
	}
	if flags.Changed("key") {
		options.HasKey = true
		if options.Key, err = flags.GetString("key"); err != nil {
			return options, err
		}
	}
	return options, nil
}

// registerFormatFlags declares the kubectl flags a listing command reads by
// hand to decide what it can number and what --match can narrow.
func registerFormatFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("output", "o", "", "Output format, as kubectl takes it; kx numbers only a table")
	cmd.Flags().Bool("no-headers", false, "Leave out the header row; kx can't number a table without one")
}

// registerWatchFlag declares --watch on the listing commands that honor it.
//
// kubectl owns the flag and isWatch parses it by hand, but kx gives it its own
// behavior — a live-redrawing table instead of a stream — so leaving it
// unregistered made a kx feature visible only in the README.
func registerWatchFlag(cmd *cobra.Command) {
	cmd.Flags().BoolP("watch", "w", false,
		"Redraw the listing live as resources change; a watch never completes, so results are not indexed")
}
