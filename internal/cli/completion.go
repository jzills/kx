package cli

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/theme"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Shell completion for kx is answered entirely from ~/.kx/state.json and the
// registries already compiled in. Nothing here calls the API server or shells
// out to kubectl: a completion runs on every Tab, and one that waits on a
// cluster is one people turn off.
//
// Everything is driven by the Use string, the same spec the help screen and
// the README table are built from. A command that declares <index> gets index
// completion by declaring it, so a new command arrives complete rather than
// falling through to cobra's default — which offers *filenames*, and did for
// every kx command but rollout.

// completer produces candidates for one argument. Candidates are "value" or
// "value\tdescription"; the shell shows the description beside the value.
type completer func(services Services, toComplete string) []string

// argCompleters maps an argument name, as it appears in a Use string, to what
// completes it. A command whose argument means something narrower registers
// "<command>.<arg>", which wins over the bare name.
var argCompleters = map[string]completer{
	"index":           completeIndex,
	"namespace.index": completeNamespaceSlot,
	"context.index":   completeContextSlot,
	"position":        completePosition,
	"resource":        completeKind,
	"top.resource":    completeTopResource,
	"action":          completeRolloutAction,
	"theme.name":      completeTheme,
	"engine.name":     completeEngine,
	"replicas":        nil, // A number kx cannot guess.
	"port":            nil, // Likewise, and it is a mapping, not a port.
	"key=value":       nil,
	"command":         nil, // Runs in the pod; local paths would be wrong.
	"src":             completePath,
	"dest":            completePath,
}

// completePath is the one case where the shell's own file completion is the
// right answer: kx cp copies between the local filesystem and a pod.
func completePath(Services, string) []string { return nil }

// installCompletions gives every command in the tree an argument completer.
func installCompletions(root *cobra.Command, services Services) {
	// The first word is nobody's argument — cobra completes command names there
	// — but `kx pods` is `kx get pods`, so the kinds belong beside them. Set
	// ahead of the walk, which fills in only what is still nil.
	root.ValidArgsFunction = rootCompletion(root, services)

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		// ValidArgs is deliberately not consulted: cobra stops completing
		// entirely once it is set, so a command using it can complete only its
		// first argument. kx sets it nowhere for that reason.
		if cmd.ValidArgsFunction == nil {
			cmd.ValidArgsFunction = argCompletion(cmd, services)
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)

	registerFlagCompletions(root, services)
}

// rootCompletion offers the kind spellings at the first word.
//
// Cobra adds its own subcommand names to what this returns rather than choosing
// between the two — "Let the logic continue so as to add any ValidArgsFunction
// completions, even if we already found sub-commands" — so `kx po<TAB>` offers
// pods beside port-forward.
//
// A spelling a command shadows is dropped. `kx secret` runs the secret command,
// the precedence rewriteKindAlias applies, so offering the spelling as well
// would list one word twice and describe it as a listing it will not produce.
func rootCompletion(root *cobra.Command, services Services) func(
	*cobra.Command, []string, string,
) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			// Past the first word, which named no command: kx has nothing to say
			// about the rest of that line, and files are still the wrong answer.
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var candidates []string
		for _, candidate := range completeKind(services, toComplete) {
			spelling, _, _ := strings.Cut(candidate, "\t")
			if shadowedByCommand(root, spelling) {
				continue
			}
			candidates = append(candidates, candidate)
		}
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
}

// argCompletion completes whichever positional argument the cursor is on.
func argCompletion(cmd *cobra.Command, services Services) func(
	*cobra.Command, []string, string,
) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Cobra completes flag values by parsing the line, which the
		// pass-through commands switch off — so for most of kx it never fires,
		// and `kx scan --engine <TAB>` offered the positional's candidates
		// instead. The flags are hand-parsed here for the same reason they are
		// hand-parsed at run time.
		if candidates, ok := flagValueCompletion(cmd, services, args, toComplete); ok {
			return candidates, cobra.ShellCompDirectiveNoFileComp
		}

		arg, ok := argAt(cmd, len(positionalsOnly(cmd, args)))
		if !ok {
			// Past the last argument the command declares. Offering nothing is
			// the honest answer, and stops the shell offering files.
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		complete, known := lookupCompleter(cmd, arg.Name)
		if !known {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if complete == nil {
			// Declared as having no candidates: kx cannot guess a replica
			// count. Files are still wrong, so say so.
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if isPathArg(arg.Name) {
			// kx cp's endpoints are half local paths, so the shell's own file
			// completion has to stay on.
			return nil, cobra.ShellCompDirectiveDefault
		}
		return complete(services, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func isPathArg(name string) bool { return name == "src" || name == "dest" }

// flagValues maps a flag name to what completes its value. Keyed by name,
// because a flag spelled the same almost always means the same thing in kx —
// every -n is a namespace, every --since is a window.
//
// --fail-on is the exception, and the reason this map grew the same
// "<command>.<flag>" precedence argCompleters uses for arguments: kx diag
// gates on a verdict and kx scan on a vulnerability severity, which share only
// the word "critical". Until a command could say so, --fail-on could not be
// completed at all and fell through to the shell's filenames — the least
// useful answer for a flag that takes one of four words.
var flagValues = map[string]completer{
	"engine":             completeEngine,
	"namespace":          completeNamespaceNames,
	"since":              completeWindow,
	"diagnostic.fail-on": completeDiagnosticThreshold,
	"scan.fail-on":       completeScanThreshold,
}

// lookupFlagValue prefers a command's own completer for a flag, the way
// lookupCompleter does for an argument.
//
// Keyed on cmd.Name(), which is the command's canonical name whatever the user
// typed: cobra resolves an alias before completing, so `kx diag --fail-on` and
// `kx diagnostic --fail-on` both land on "diagnostic".
func lookupFlagValue(cmd *cobra.Command, name string) (completer, bool) {
	if complete, ok := flagValues[cmd.Name()+"."+name]; ok {
		return complete, true
	}
	complete, ok := flagValues[name]
	return complete, ok
}

// flagValueCompletion answers when the cursor is on a flag's value rather than
// on a positional argument: either after the flag ("--engine ") or attached to
// it ("--engine=tri"). Reports false when the line is not a flag value, so the
// caller falls through to the positional path.
func flagValueCompletion(
	cmd *cobra.Command, services Services, args []string, toComplete string,
) ([]string, bool) {
	if name, prefix, found := strings.Cut(toComplete, "="); found && strings.HasPrefix(name, "--") {
		complete, ok := lookupFlagValue(cmd, strings.TrimPrefix(name, "--"))
		if !ok {
			return nil, false
		}
		// The shell replaces the whole word, so each candidate has to carry
		// the "--flag=" it is completing after.
		var candidates []string
		for _, candidate := range complete(services, prefix) {
			candidates = append(candidates, name+"="+candidate)
		}
		return candidates, true
	}

	if len(args) == 0 {
		return nil, false
	}
	flag := valueFlag(cmd, args[len(args)-1])
	if flag == "" {
		return nil, false
	}
	complete, ok := lookupFlagValue(cmd, flag)
	if !ok {
		// A flag that takes a value, but not one kx can suggest values for —
		// a label selector, say. Still not a filename.
		return nil, true
	}
	return complete(services, toComplete), true
}

// valueFlag returns the registered name of a flag token that takes a value, or
// "" for anything else: a positional, a switch, or a flag kx doesn't register.
func valueFlag(cmd *cobra.Command, token string) string {
	if !strings.HasPrefix(token, "-") || token == "-" || token == "--" {
		return ""
	}
	var flag *pflag.Flag
	if name, ok := strings.CutPrefix(token, "--"); ok {
		flag = cmd.Flags().Lookup(name)
	} else if shorthand := strings.TrimPrefix(token, "-"); len(shorthand) == 1 {
		flag = cmd.Flags().ShorthandLookup(shorthand)
	}
	if flag == nil || flag.Value.Type() == "bool" {
		return ""
	}
	return flag.Name
}

// positionalsOnly drops the flag tokens from a command line so the positional
// being completed is counted correctly.
//
// The pass-through commands see kubectl's flags as well as their own, and kx
// cannot know how many values an unregistered flag takes. One is assumed for
// the flags kx registers and none for the rest, which is right for every
// spelling kx itself defines and wrong only for an unregistered kubectl flag
// with a detached value — where the cost is a completion list that would have
// been offered one argument later.
func positionalsOnly(cmd *cobra.Command, args []string) []string {
	var positionals []string
	for i := 0; i < len(args); i++ {
		token := args[i]
		if !strings.HasPrefix(token, "-") || token == "-" {
			positionals = append(positionals, token)
			continue
		}
		if token == "--" {
			// Everything after -- is the command kx exec runs, not kx's.
			break
		}
		if !strings.Contains(token, "=") && valueFlag(cmd, token) != "" {
			i++
		}
	}
	return positionals
}

// lookupCompleter prefers a command's own completer for an argument name.
func lookupCompleter(cmd *cobra.Command, name string) (completer, bool) {
	if complete, ok := argCompleters[cmd.Name()+"."+name]; ok {
		return complete, true
	}
	complete, ok := argCompleters[name]
	return complete, ok
}

// argAt returns the argument at a position, accounting for a repeatable last
// one: `kx describe 1 2 3` is still completing `index` at position three.
func argAt(cmd *cobra.Command, position int) (Arg, bool) {
	args := ParseUse(cmd.Use).Args
	if len(args) == 0 {
		return Arg{}, false
	}
	if position < len(args) {
		return args[position], true
	}
	if last := args[len(args)-1]; last.Variadic {
		return last, true
	}
	return Arg{}, false
}

// completeIndex offers the rows of the current listing, described by what they
// point at — the whole reason indexes are worth completing, since "3" on its
// own tells a reader nothing.
func completeIndex(services Services, _ string) []string {
	entry, err := loadCurrent(services)
	if err != nil {
		return nil
	}
	return indexCandidates(entry)
}

func indexCandidates(entry state.State) []string {
	var candidates []string
	for position, resource := range entry.Resources.Entries() {
		label := resource.Name
		if resource.Kind != "" {
			label += " (" + string(resource.Kind) + ")"
		}
		candidates = append(candidates, strconv.Itoa(position+1)+"\t"+label)
	}
	return candidates
}

// completeNamespaceSlot and completeContextSlot read the switch listings,
// which live outside the history stack — the same slots `kx ns 2` resolves
// against, so a completion can't disagree with what the number will do.
func completeNamespaceSlot(services Services, _ string) []string {
	return slotCandidates(services, kinds.Namespace)
}

func completeContextSlot(services Services, _ string) []string {
	return slotCandidates(services, kinds.Context)
}

func slotCandidates(services Services, kind kinds.Kind) []string {
	if services.State == nil {
		return nil
	}
	history, err := services.State.LoadHistory()
	if err != nil {
		return nil
	}
	entry, ok := history.Named[kind]
	if !ok {
		return nil
	}
	var candidates []string
	for position, resource := range entry.Resources.Entries() {
		candidates = append(candidates, strconv.Itoa(position+1)+"\t"+resource.Name)
	}
	return candidates
}

// completePosition offers history positions, described by the query that
// produced each one, which is how kx state --all identifies them.
func completePosition(services Services, _ string) []string {
	if services.State == nil {
		return nil
	}
	history, err := services.State.LoadHistory()
	if err != nil {
		return nil
	}
	var candidates []string
	for position, entry := range history.States {
		label := entry.Namespace
		if entry.Query != nil {
			label = entry.Query.Resource
			if entry.Namespace != "" {
				label += " in " + entry.Namespace
			}
		}
		if label == "" {
			label = "listing"
		}
		candidates = append(candidates, strconv.Itoa(position+1)+"\t"+label)
	}
	return candidates
}

// completeKind offers the resource spellings kx resolves, described by the
// kind each maps to so the shorthands are self-explaining.
func completeKind(_ Services, _ string) []string {
	var candidates []string
	for _, spelling := range kinds.Spellings() {
		candidates = append(candidates, spelling.Name+"\t"+string(spelling.Kind))
	}
	return candidates
}

func completeTopResource(Services, string) []string {
	return []string{"pods\tCPU and memory per pod", "nodes\tCPU and memory per node"}
}

func completeRolloutAction(Services, string) []string {
	candidates := make([]string, 0, len(rolloutActions))
	for _, action := range rolloutActions {
		candidates = append(candidates, action.Name+"\t"+action.Doc)
	}
	return candidates
}

func completeTheme(Services, string) []string {
	names := theme.Names()
	sort.Strings(names)
	return names
}

func completeEngine(Services, string) []string {
	return scanner.Names()
}

// completeWindow suggests the windows --since is documented with — the same
// list the help strings name, split rather than restated, so the shell can
// never offer a vocabulary the help does not teach.
//
// Not a closed set — any duration config.ParseDuration reads is legal — but
// the shell's fallback for a flag with no completer of its own is filenames,
// which is what a duration flag least wants. The day spelling in particular is
// kx's own, since kubectl's --since rejects "7d", so a reader who is never
// offered it has no way to learn from the shell that it exists.
func completeWindow(Services, string) []string {
	return strings.Split(config.DurationExamples, ", ")
}

// completeDiagnosticThreshold offers the verdicts kx diag's --fail-on accepts,
// read from the map the flag is validated against so the shell cannot suggest
// a word the flag would reject.
//
// One spelling per threshold. "warnings" parses too — a verdict prints as
// "Deployment/api · warnings", so anyone reading one and typing it back is
// accommodated — but offering both would list four choices where there are
// three, and Token() is the spelling the document uses.
//
// Most severe first, matching how the flag's own help and error name them, and
// how kx orders severities everywhere else.
func completeDiagnosticThreshold(Services, string) []string {
	seen := map[diagnostics.Severity]bool{}
	severities := make([]diagnostics.Severity, 0, len(diagnosticThresholds))
	for _, severity := range diagnosticThresholds {
		if !seen[severity] {
			seen[severity] = true
			severities = append(severities, severity)
		}
	}
	sort.Slice(severities, func(i, j int) bool { return severities[i] > severities[j] })

	candidates := make([]string, 0, len(severities))
	for _, severity := range severities {
		candidates = append(candidates,
			severity.Token()+"\tExit 2 on "+severity.Token()+" or worse")
	}
	return candidates
}

// completeScanThreshold offers the vulnerability severities kx scan's --fail-on
// accepts, read from scanner.Severities for the same reason.
//
// UNSPECIFIED is dropped exactly where the validator drops it: it is a bucket
// rather than a level, so "fail on unspecified or worse" means nothing.
// Lowercased because that is how the flag's help spells them, and the parser
// upper-cases whatever it is given.
func completeScanThreshold(Services, string) []string {
	candidates := make([]string, 0, len(scanner.Severities))
	for _, severity := range scanner.Severities {
		if severity == "UNSPECIFIED" {
			continue
		}
		name := strings.ToLower(severity)
		candidates = append(candidates, name+"\tExit 2 on "+name+" or worse")
	}
	return candidates
}

// registerFlagCompletions completes flag values that come from a fixed set or
// from state, for every command that registers the flag.
//
// Driven by each command's own flags rather than by the keys of flagValues,
// which is what lets a key be qualified: "diagnostic.fail-on" is not a flag
// name and looking it up as one would find nothing.
func registerFlagCompletions(root *cobra.Command, services Services) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			complete, ok := lookupFlagValue(cmd, flag.Name)
			if !ok || complete == nil {
				return
			}
			// The error is ignored deliberately: an inherited persistent flag
			// is the same *pflag.Flag on parent and child, and cobra refuses
			// the second registration. The parent's is already correct.
			_ = cmd.RegisterFlagCompletionFunc(flag.Name, func(
				_ *cobra.Command, _ []string, toComplete string,
			) ([]string, cobra.ShellCompDirective) {
				return complete(services, toComplete), cobra.ShellCompDirectiveNoFileComp
			})
		})
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// completeNamespaceNames offers namespace names rather than indexes: -n takes
// a name. Read from the same slot kx ns fills, so it is populated by having
// run kx ns at least once and costs no API call.
func completeNamespaceNames(services Services, _ string) []string {
	if services.State == nil {
		return nil
	}
	history, err := services.State.LoadHistory()
	if err != nil {
		return nil
	}
	entry, ok := history.Named[kinds.Namespace]
	if !ok {
		return nil
	}
	return entry.Resources.Names()
}

// loadCurrent reads the entry at the history cursor, which is what every index
// on the command line resolves against.
func loadCurrent(services Services) (state.State, error) {
	if services.State == nil {
		return state.State{}, state.ErrNoState
	}
	return services.State.Load()
}
