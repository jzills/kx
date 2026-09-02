package cli

import (
	"sort"
	"strconv"
	"strings"

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

// flagValues maps a flag name to what completes its value. Keyed by name
// rather than by command because a flag spelled the same means the same thing
// everywhere in kx — every -n is a namespace.
var flagValues = map[string]completer{
	"engine":    completeEngine,
	"namespace": completeNamespaceNames,
}

// flagValueCompletion answers when the cursor is on a flag's value rather than
// on a positional argument: either after the flag ("--engine ") or attached to
// it ("--engine=tri"). Reports false when the line is not a flag value, so the
// caller falls through to the positional path.
func flagValueCompletion(
	cmd *cobra.Command, services Services, args []string, toComplete string,
) ([]string, bool) {
	if name, prefix, found := strings.Cut(toComplete, "="); found && strings.HasPrefix(name, "--") {
		complete, ok := flagValues[strings.TrimPrefix(name, "--")]
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
	complete, ok := flagValues[flag]
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

// registerFlagCompletions completes flag values that come from a fixed set or
// from state, for every command that registers the flag.
func registerFlagCompletions(root *cobra.Command, services Services) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for name, complete := range flagValues {
			if cmd.Flags().Lookup(name) == nil {
				continue
			}
			_ = cmd.RegisterFlagCompletionFunc(name, func(
				_ *cobra.Command, _ []string, toComplete string,
			) ([]string, cobra.ShellCompDirective) {
				return complete(services, toComplete), cobra.ShellCompDirectiveNoFileComp
			})
		}
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
