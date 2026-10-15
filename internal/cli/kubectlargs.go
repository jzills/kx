package cli

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jzills/kx/internal/kubectlflags"
)

// A command that forwards to kubectl can register kubectl's own flags, read
// from the generated table in internal/kubectlflags, and let cobra parse its
// argv instead of splitting it by hand (see passthrough.go for the commands
// that still do).
//
// pflag then handles every spelling kubectl accepts — `-n x`, `-nx`,
// `--namespace=x`, `-Aw`, `--` — and finds positionals wherever they sit, so
// `kx get -n prod pods` lists pods rather than saving every row with kind
// "-n", and in `kx get pods -A 1` the 1 is an index, because the table says -A
// takes no value.
//
// Each kubectl flag's Value records the occurrence into a kubectlArgs, in the
// order pflag meets them, so what reaches kubectl is what the user gave it:
// repeats kept, values untouched. A flag kx also reads (-n, -A, -o, -w,
// --no-headers on kx get) is read back out of that argv by the same helpers as
// before, so converting a command changes how argv is split and nothing after.

// kubectlFlagAnnotation marks a flag registered from kubectl's table. Its value
// says where help lists it: kubectlFlagCommand for the subcommand's own flags,
// kubectlFlagGlobal for kubectl's global ones. A flag kx documents itself
// (shared, with kx's own usage) carries no annotation and is listed with kx's
// options.
const (
	kubectlFlagAnnotation = "kx/kubectl-flag"
	kubectlFlagCommand    = "command"
	kubectlFlagGlobal     = "global"
)

// kubectlCommandAnnotation names, on a command, the kubectl subcommand whose
// flags it registered. Help reads it to stop claiming unknown flags pass
// through: on such a command, they are refused.
const kubectlCommandAnnotation = "kx/kubectl-command"

// kubectlArgs collects the kubectl flags given to one command, in order.
type kubectlArgs struct {
	tokens []string
	// definedBy names, for each subcommand flag registered, the subcommands
	// that define it, on a command that forwards to more than one (kx top:
	// top pod or top node). Global flags are not in it: every subcommand
	// takes them.
	definedBy map[string][]string
}

// definers names, as "kubectl <subcommand>", the subcommands that define the
// flag spelled "--name", for a refusal from outside to say where it belongs.
func (a *kubectlArgs) definers(flag string) []string {
	var names []string
	for _, subcommand := range a.definedBy[strings.TrimPrefix(flag, "--")] {
		names = append(names, "kubectl "+subcommand)
	}
	return names
}

// outside returns the first kubectl flag given on the command that subcommand
// does not define, by its long name, or "" when there is none. A command that
// registered the flags of several subcommands calls it once it knows which
// one it will run, so a flag of the other is refused in kx's words rather than
// kubectl's.
func (a *kubectlArgs) outside(flags *pflag.FlagSet, subcommand string) string {
	found := ""
	flags.Visit(func(flag *pflag.Flag) {
		definers, ok := a.definedBy[flag.Name]
		if found == "" && ok && !slices.Contains(definers, subcommand) {
			found = "--" + flag.Name
		}
	})
	return found
}

// add records one occurrence of flag with value, spelled the way kx forwards
// every flag: the long name, so a refusal quoting it reads the same as kx tree
// and kx diag already do, and the value attached with "=" so a value beginning
// with "-" cannot be read as a flag of its own. A flag given in its bare form
// is forwarded bare, which is what kubectl defaults it from.
func (a *kubectlArgs) add(flag kubectlflags.Flag, value string) {
	if flag.NoOptDefVal != "" && value == flag.NoOptDefVal {
		a.tokens = append(a.tokens, "--"+flag.Name)
		return
	}
	a.tokens = append(a.tokens, "--"+flag.Name+"="+value)
}

// kubectlValue is the pflag.Value of a kubectl flag: it records each Set rather
// than holding a typed value, since kubectl, not kx, validates it.
type kubectlValue struct {
	args  *kubectlArgs
	flag  kubectlflags.Flag
	value string
	// kxType is the type kx registered a shared flag with, which help shows
	// in place of kubectl's: kx documents a shared flag in its own terms, and
	// kx logs' --since takes 7d, which is no Go duration.
	kxType string
}

func (v *kubectlValue) Set(value string) error {
	v.value = value
	v.args.add(v.flag, value)
	return nil
}

func (v *kubectlValue) String() string { return v.value }

// Type is kubectl's own, so help names the value the way kubectl's does —
// or, for a flag kx documents itself, the type kx gave it.
func (v *kubectlValue) Type() string {
	if v.kxType != "" {
		return v.kxType
	}
	return v.flag.Type
}

// registerKubectlFlags registers every flag kubectl defines on subcommands,
// and kubectl's global flags, on cmd, and returns the argv they record. A
// command that runs one of several subcommands, decided after parsing (kx top:
// top pod or top node), registers all of them and asks the result which flags
// fall outside the one it chose (see outside).
//
// A flag cmd already registered is shared: kx reads it as well as forwarding
// it. It keeps kx's usage text, which says what kx does with it, and takes
// kubectl's shape, so it parses exactly as kubectl would. A shorthand cmd
// already gives to a flag of its own stays kx's — kx get's -k is --key — and
// kubectl's flag is registered by its long name alone.
//
// Flag names are normalized as kubectl normalizes them, so --all_namespaces
// is --all-namespaces here as it is there.
func registerKubectlFlags(cmd *cobra.Command, subcommands ...string) *kubectlArgs {
	args := &kubectlArgs{definedBy: map[string][]string{}}
	flags := cmd.Flags()
	flags.SetNormalizeFunc(wordSepNormalize)
	register := func(flag kubectlflags.Flag, where string) {
		registerKubectlFlag(flags, args, flag, where)
	}
	for _, subcommand := range subcommands {
		own, ok := kubectlflags.Command(subcommand)
		if !ok {
			panic("kubectlflags has no table for kubectl " + subcommand)
		}
		for _, flag := range own {
			// The first subcommand to define a flag registers it; the
			// others only add themselves to its definers.
			if _, seen := args.definedBy[flag.Name]; !seen {
				register(flag, kubectlFlagCommand)
			}
			args.definedBy[flag.Name] = append(args.definedBy[flag.Name], subcommand)
		}
	}
	for _, flag := range kubectlflags.Global() {
		// A subcommand's flag shadows a global one of the same name — kubectl
		// debug's --profile — and is already registered by the loop above.
		if _, shadowed := args.definedBy[flag.Name]; shadowed {
			continue
		}
		register(flag, kubectlFlagGlobal)
	}

	// Copied, never written in place: several commands share one annotation
	// map (documentAnnotations, mutatingAnnotations), and marking it here
	// marked every command sharing it as converted.
	annotations := maps.Clone(cmd.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[kubectlCommandAnnotation] = strings.Join(subcommands, ",")
	cmd.Annotations = annotations
	cmd.SetFlagErrorFunc(flagError)
	return args
}

// registerKubectlFlag adds one flag from kubectl's table to a cobra flag set.
// A shorthand collision keeps the long flag but not the shorthand, as before.
func registerKubectlFlag(flags *pflag.FlagSet, args *kubectlArgs, flag kubectlflags.Flag, where string) {
	value := &kubectlValue{args: args, flag: flag}
	if existing := flags.Lookup(flag.Name); existing != nil {
		// kx's alone (ownFlag): kx builds it into the argv itself, so
		// it is neither forwarded as given nor listed as kubectl's.
		if len(existing.Annotations[kxOwnedAnnotation]) > 0 {
			return
		}
		value.kxType = existing.Value.Type()
		existing.Value = value
		existing.NoOptDefVal = flag.NoOptDefVal
		existing.DefValue = ""
		if existing.Shorthand == flag.Shorthand {
			existing.ShorthandDeprecated = flag.ShorthandDeprecated
		}
		return
	}
	shorthand := flag.Shorthand
	if shorthand != "" && flags.ShorthandLookup(shorthand) != nil {
		shorthand = ""
	}
	registered := flags.VarPF(value, flag.Name, shorthand, flag.Usage)
	registered.NoOptDefVal = flag.NoOptDefVal
	registered.Deprecated = flag.Deprecated
	registered.ShorthandDeprecated = flag.ShorthandDeprecated
	registered.Hidden = flag.Hidden
	registered.Annotations = map[string][]string{kubectlFlagAnnotation: {where}}
	// kubectl's mark for a flag that takes a file, which cobra's
	// completion reads to offer files, narrowed to the extensions.
	if flag.Filename {
		registered.Annotations[cobra.BashCompFilenameExt] = flag.Extensions
	}
}

// recordedKubectlArgs is cmd's recorder, for code that sees the command but
// not its RunE's closure — machineOutput, deciding from the argv kubectl will
// get whether a failure may print a listing to stdout — or nil for a command
// that still splits its argv by hand.
//
// Read from cmd's own flags, each of which points at the recorder, rather than
// from a process-wide map keyed by command, which kept every root ever built
// reachable.
func recordedKubectlArgs(cmd *cobra.Command) *kubectlArgs {
	if cmd.Annotations[kubectlCommandAnnotation] == "" {
		return nil
	}
	var recorded *kubectlArgs
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if value, ok := flag.Value.(*kubectlValue); ok && recorded == nil {
			recorded = value.args
		}
	})
	return recorded
}

// kxOwnedAnnotation marks a flag kx registered under one of kubectl's names
// that kx reads and puts into kubectl's argv itself (see ownFlag).
const kxOwnedAnnotation = "kx/owned"

// ownFlag marks the flag name, already registered on cmd, as kx's alone though
// kubectl defines one by that name: kx label reads --overwrite and adds it to
// the argv it builds, so forwarding it as given as well would send it twice.
// Called before registerKubectlFlags.
func ownFlag(cmd *cobra.Command, name string) {
	flag := cmd.Flags().Lookup(name)
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[kxOwnedAnnotation] = []string{"true"}
}

// isKubectlFlag reports whether flag was registered from kubectl's table
// rather than by kx.
func isKubectlFlag(flag *pflag.Flag) bool { return kubectlFlagScope(flag) != "" }

// IsKubectlFlag is isKubectlFlag for the doc generators, which list kx's own
// flags in a command's signature and leave kubectl's to the placeholder.
func IsKubectlFlag(flag *pflag.Flag) bool { return isKubectlFlag(flag) }

// kubectlFlagScope is kubectlFlagCommand or kubectlFlagGlobal for a flag
// registered from kubectl's table, "" for one of kx's.
func kubectlFlagScope(flag *pflag.Flag) string {
	if scope := flag.Annotations[kubectlFlagAnnotation]; len(scope) > 0 {
		return scope[0]
	}
	return ""
}

// wordSepNormalize is kubectl's flag-name normalization (component-base's
// WordSepNormalizeFunc): an underscore is a dash.
func wordSepNormalize(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
}

// flagError puts the two flag errors pflag raises on a converted command into
// kx's words.
//
// An unknown flag: the flag table is read from one kubectl release, so a flag
// newer than it is unknown here though the kubectl on PATH would take it.
// Anything after `--` reaches kubectl without being checked against the table,
// which is the way through; kx --version names both kubectls when the
// difference needs explaining.
//
// A malformed value for one of kx's own typed flags (kx top's --port): the
// same sentence kx uses for a malformed index. kubectl's flags never raise it,
// since kx forwards their values for kubectl to judge.
func flagError(cmd *cobra.Command, err error) error {
	var invalid *pflag.InvalidValueError
	if errors.As(err, &invalid) {
		return fmt.Errorf("Invalid value for '--%s': '%s' is not a valid %s.",
			invalid.GetFlag().Name, invalid.GetValue(), invalid.GetFlag().Value.Type())
	}
	var missing *pflag.NotExistError
	if !errors.As(err, &missing) {
		return err
	}
	spelling := "--" + missing.GetSpecifiedName()
	if missing.GetSpecifiedShortnames() != "" {
		spelling = "-" + missing.GetSpecifiedName()
	}
	if trailingCommand(cmd) {
		return fmt.Errorf(
			"'%s' isn't a flag of %s as of %s, the release kx's table of its "+
				"flags was read from. Everything after -- is the command to run, "+
				"so a flag kx doesn't know needs %s itself.",
			spelling, kubectlSubcommands(cmd), kubectlflags.Version, kubectlSubcommands(cmd))
	}
	return fmt.Errorf(
		"'%s' isn't a flag of %s as of %s, the release kx's table of its flags "+
			"was read from. If your kubectl takes it, put it after --: '%s -- %s'.",
		spelling, kubectlSubcommands(cmd), kubectlflags.Version, requiredUse(cmd), spelling)
}

// kubectlSubcommands names the kubectl subcommands cmd forwards to: "kubectl
// get", or, for several under one parent, the parent once — "kubectl top pod
// or node", "kubectl rollout status, restart, pause, resume, history or undo".
func kubectlSubcommands(cmd *cobra.Command) string {
	subcommands := strings.Split(cmd.Annotations[kubectlCommandAnnotation], ",")
	if len(subcommands) == 1 {
		return "kubectl " + subcommands[0]
	}
	parent, _, _ := strings.Cut(subcommands[0], " ")
	children := make([]string, 0, len(subcommands))
	for _, subcommand := range subcommands {
		child, ok := strings.CutPrefix(subcommand, parent+" ")
		if !ok {
			// No shared parent: name each in full.
			named := make([]string, len(subcommands))
			for i, s := range subcommands {
				named[i] = "kubectl " + s
			}
			return joinOr(named)
		}
		children = append(children, child)
	}
	return "kubectl " + parent + " " + joinOr(children)
}

// joinOr lists names as "a, b or c".
func joinOr(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// requiredUse is cmd's path and its required arguments, the shortest
// invocation an example can be built on: "kx get <resource>".
func requiredUse(cmd *cobra.Command) string {
	parts := []string{cmd.CommandPath()}
	for _, arg := range ParseUse(cmd.Use).Args {
		if arg.Required {
			parts = append(parts, "<"+arg.Name+">")
		}
	}
	return strings.Join(parts, " ")
}

// kubectlArgv splits what cobra parsed into the positional arguments and the
// argv for kubectl: the recorded flags, then anything after `--`, unparsed. On a
// command that runs a command of its own after `--` (see trailingCommand),
// that is the command, not kubectl's, and is left for afterDash.
func kubectlArgv(cmd *cobra.Command, args []string, recorded *kubectlArgs) (positional, forward []string) {
	positional = args
	var verbatim []string
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		positional, verbatim = args[:at], args[at:]
	}
	if trailingCommand(cmd) {
		verbatim = nil
	}
	forward = append(append([]string{}, recorded.tokens...), verbatim...)
	return positional, forward
}

// afterDash is what follows `--` in args, nil when there is no `--`.
func afterDash(cmd *cobra.Command, args []string) []string {
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		return args[at:]
	}
	return nil
}

// trailingCommand reports whether cmd takes a command to run after `--` — kx
// exec and kx debug, whose Use ends "[-- command...]". There `--` cannot
// also be the way to hand kubectl a flag kx doesn't know.
func trailingCommand(cmd *cobra.Command) bool {
	for _, arg := range ParseUse(cmd.Use).Args {
		if arg.Name == "command" {
			return true
		}
	}
	return false
}

// indexArgv splits a converted index command's parsed argv: the leading run of
// indexes, and kubectl's argv — any positional after the indexes, ahead of
// kubectl's flags, as it was when kx split argv by hand (kx logs 1 web names a
// container). The first positional is always an index, so a non-numeric one
// is reported rather than quietly forwarded.
func indexArgv(cmd *cobra.Command, args []string, recorded *kubectlArgs) (indexes, extra []string, err error) {
	indexes, stray, forward, err := splitIndexArgv(cmd, args, recorded)
	if err != nil {
		return nil, nil, err
	}
	return indexes, append(stray[:len(stray):len(stray)], forward...), nil
}

// splitIndexArgv is indexArgv with the positionals after the indexes kept
// apart from kubectl's flags, for a command that refuses them (refuseStray).
func splitIndexArgv(cmd *cobra.Command, args []string, recorded *kubectlArgs) (indexes, stray, forward []string, err error) {
	positional, forward := kubectlArgv(cmd, args, recorded)
	indexes, stray = splitLeadingIndexes(positional)
	if len(indexes) == 0 {
		if len(positional) > 0 {
			return nil, nil, nil, fmt.Errorf(
				"Invalid value for 'indexes': '%s' is not a valid int.", positional[0])
		}
		return nil, nil, nil, requiredArgsError(cmd)
	}
	return indexes, stray, forward, nil
}

// refuseStray refuses a positional left over after the arguments a mutating
// command takes. Forwarded, kubectl reads it as one more resource to act on —
// kx delete 3 web deletes web too — which neither the confirm prompt nor the
// report ever named. A flag whose value is optional, given bare (--cascade
// orphan), leaves its intended value here, so the refusal says how to attach
// it. Anything after -- is still forwarded unread.
func refuseStray(cmd *cobra.Command, recorded *kubectlArgs, stray []string) error {
	if len(stray) == 0 {
		return nil
	}
	word := stray[0]
	if flag := bareOptionalValue(cmd, recorded); flag != "" {
		return fmt.Errorf(
			"'%s' isn't an argument of %s. If it is %s's value, attach it: '%s=%s'.",
			word, cmd.CommandPath(), flag, flag, word)
	}
	return fmt.Errorf(
		"'%s' isn't an argument of %s — kubectl would act on it as another resource, "+
			"one kx never named. See '%s --help' for usage.",
		word, cmd.CommandPath(), cmd.CommandPath())
}

// bareOptionalValue is the last kubectl flag recorded in its bare form that
// takes a value when one is attached (--cascade, --dry-run), or "" when none
// was. A boolean's bare form is complete, so it is not one.
func bareOptionalValue(cmd *cobra.Command, recorded *kubectlArgs) string {
	for i := len(recorded.tokens) - 1; i >= 0; i-- {
		token := recorded.tokens[i]
		if strings.Contains(token, "=") {
			continue
		}
		flag := cmd.Flags().Lookup(strings.TrimPrefix(token, "--"))
		if flag != nil && flag.NoOptDefVal != "" && flag.Value.Type() != "bool" {
			return token
		}
	}
	return ""
}
