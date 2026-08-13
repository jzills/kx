package cli

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// completionServices builds a Services whose state file is a temp file seeded
// with a listing, so completion is exercised against real saved state rather
// than a fake.
func completionServices(t *testing.T) Services {
	t.Helper()
	service := state.NewService(10)
	service.Path = filepath.Join(t.TempDir(), "state.json")

	err := service.Save(state.State{
		Resources: state.NewResources([]string{"api-7d8f", "web-2c4a"}, kinds.Pod),
		Namespace: "prod",
		Query:     &state.Query{Resource: "pods"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	err = service.SaveNamed(state.State{
		Resources: state.NewResources([]string{"default", "prod"}, kinds.Namespace),
	})
	if err != nil {
		t.Fatalf("SaveNamed: %v", err)
	}
	return Services{State: service}
}

func complete(t *testing.T, root *cobra.Command, args ...string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	toComplete := args[len(args)-1]
	path := args[:len(args)-1]

	cmd, rest, err := root.Find(path)
	if err != nil {
		t.Fatalf("root.Find(%v): %v", path, err)
	}
	if cmd.ValidArgsFunction == nil {
		t.Fatalf("%s has no completion function", cmd.CommandPath())
	}
	return cmd.ValidArgsFunction(cmd, rest, toComplete)
}

// An index is the argument kx is built around and the one worth completing:
// "3" alone says nothing, so each candidate carries the resource it resolves
// to.
func TestIndexCompletionNamesTheResources(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, directive := complete(t, root, "describe", "")

	if len(candidates) != 2 {
		t.Fatalf("candidates = %v, want one per saved resource", candidates)
	}
	if candidates[0] != "1\tapi-7d8f (Pod)" {
		t.Errorf("candidates[0] = %q, want the index described by its resource", candidates[0])
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// Repeatable arguments keep completing: `kx delete 1 <TAB>` is still choosing
// an index.
func TestIndexCompletionContinuesForRepeatableArgs(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, _ := complete(t, root, "delete", "1", "")
	if len(candidates) != 2 {
		t.Errorf("candidates after the first index = %v, want the listing again", candidates)
	}
}

// Before this, every kx command but rollout fell through to cobra's default,
// which offers filenames — `kx describe <TAB>` listed the current directory.
func TestNoCommandOffersFileCompletion(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	// kx cp is the exception: its endpoints are local paths.
	pathCommands := map[string]bool{"kx cp": true}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.ValidArgsFunction == nil {
			// Without one, cobra falls back to file completion — which is the
			// whole defect. An unanswerable argument still needs a completer
			// that says so.
			t.Errorf("%s has no completion function", cmd.CommandPath())
		} else {
			_, directive := cmd.ValidArgsFunction(cmd, nil, "")
			wantFiles := pathCommands[cmd.CommandPath()]
			gotFiles := directive == cobra.ShellCompDirectiveDefault
			if gotFiles != wantFiles {
				t.Errorf("%s: file completion = %v, want %v", cmd.CommandPath(), gotFiles, wantFiles)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// Cobra stops completing once ValidArgs is set — it covers the first argument
// and nothing else, and the ValidArgsFunction beside it is never called. kx
// completes every position, so nothing may set ValidArgs.
func TestNoCommandSetsValidArgs(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if len(cmd.ValidArgs) > 0 {
			t.Errorf("%s sets ValidArgs %v, which stops cobra completing later arguments",
				cmd.CommandPath(), cmd.ValidArgs)
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// Both positions of `kx rollout <action> <index>` complete. Cobra's ValidArgs
// could only ever have covered the first, which is why kx no longer sets it.
func TestRolloutCompletesBothArguments(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	actions, _ := complete(t, root, "rollout", "")
	if len(actions) != len(rolloutActions) {
		t.Errorf("actions = %v, want one per rollout action", actions)
	}
	if !strings.HasPrefix(actions[0], "status\t") {
		t.Errorf("actions[0] = %q, want status described", actions[0])
	}

	indexes, _ := complete(t, root, "rollout", "status", "")
	if len(indexes) != 2 {
		t.Errorf("second argument = %v, want the saved listing", indexes)
	}
}

// `kx ns 2` resolves against the namespace slot, not the history cursor, so a
// completion reading the cursor would offer numbers that mean something else.
func TestNamespaceCompletionReadsItsOwnSlot(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, _ := complete(t, root, "namespace", "")

	if len(candidates) != 2 {
		t.Fatalf("candidates = %v, want the saved namespaces", candidates)
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "api-7d8f") {
			t.Errorf("namespace completion offered a pod from the history stack: %q", candidate)
		}
	}
	if candidates[1] != "2\tprod" {
		t.Errorf("candidates[1] = %q, want 2\tprod", candidates[1])
	}
}

// The pass-through commands disable cobra's flag parsing, so cobra never
// completes their flag values; kx parses them here the way it does at run time.
func TestFlagValuesCompleteOnPassthroughCommands(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	namespaces, _ := complete(t, root, "get", "-n", "")
	if strings.Join(namespaces, " ") != "default prod" {
		t.Errorf("kx get -n <TAB> = %v, want the saved namespaces", namespaces)
	}

	engines, _ := complete(t, root, "scan", "--engine", "")
	if len(engines) == 0 || engines[0] != "scout" {
		t.Errorf("kx scan --engine <TAB> = %v, want the scan engines", engines)
	}

	// The shell replaces the whole word, so an attached value has to come back
	// with its flag still on the front.
	attached, _ := complete(t, root, "scan", "--engine=")
	if len(attached) == 0 || attached[0] != "--engine=scout" {
		t.Errorf("kx scan --engine=<TAB> = %v, want candidates carrying the flag", attached)
	}
}

// A flag and its value must not be counted as positional arguments, or the
// completer answers for the wrong one.
func TestFlagsDoNotShiftTheArgumentPosition(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	// `kx get <resource> [index]...`: with -n and its value counted as
	// positionals, the cursor lands on the repeatable <index> and kx offers
	// the listing instead of the resource type that has not been typed yet.
	candidates, _ := complete(t, root, "get", "-n", "prod", "")

	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, "1\t") {
			t.Fatalf("kx get -n prod <TAB> offered an index: %v", candidates)
		}
	}
	found := false
	for _, candidate := range candidates {
		if candidate == "pods\tPod" {
			found = true
		}
	}
	if !found {
		t.Errorf("kx get -n prod <TAB> = %v, want the resource types", candidates)
	}
}

// kx get takes a resource type first; the shorthands are only useful if the
// kind each maps to comes with them.
func TestResourceCompletionDescribesTheKinds(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, _ := complete(t, root, "get", "")

	found := false
	for _, candidate := range candidates {
		if candidate == "deploy\tDeployment" {
			found = true
		}
	}
	if !found {
		t.Errorf("kx get <TAB> = %v..., want shorthands described by their kind", candidates[:min(5, len(candidates))])
	}
}

// `kx po <TAB>` completes the same as `kx get po <TAB>`, because `kx po` runs
// the same command.
//
// Driven through Execute and cobra's own dispatch rather than the complete()
// helper above, because that is where this failed: the helper resolves the
// command itself, so it cannot see cobra resolving a bare kind spelling to no
// command at all — which answered with the shell's *filename* completion.
func TestCompletionFollowsTheKindShorthand(t *testing.T) {
	for _, line := range [][]string{
		{"po", ""},        // kx po <TAB>
		{"pods", "1", ""}, // kx pods 1 <TAB>
		{"po", "-n", ""},  // kx po -n <TAB>
		{"get", "po", ""}, // and the spelled-out form still works
	} {
		root := NewRoot(completionServices(t), "test")
		var out bytes.Buffer
		root.SetOut(&out)

		args := append([]string{cobra.ShellCompRequestCmd}, line...)
		if err := Execute(root, args); err != nil {
			t.Fatalf("Execute(%v): %v", args, err)
		}

		want := "1\tapi-7d8f (Pod)"
		if line[len(line)-2] == "-n" {
			want = "prod"
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("kx %s<TAB> completed with %q, want %q",
				strings.Join(line, " "), out.String(), want)
		}
		// Without this the shell falls back to offering filenames, which is
		// what the missing rewrite actually produced.
		if !strings.Contains(out.String(), ":"+strconv.Itoa(int(cobra.ShellCompDirectiveNoFileComp))) {
			t.Errorf("kx %s<TAB> directive in %q, want NoFileComp",
				strings.Join(line, " "), out.String())
		}
	}
}

// `kx pods` is `kx get pods`, so the first word completes to the resource types
// as well as to the command names cobra offers there on its own. Without this
// the shorthand could only be completed by someone who had already typed it out
// in full.
func TestRootCompletionOffersKindSpellings(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, directive := complete(t, root, "")

	found := false
	for _, candidate := range candidates {
		if candidate == "pods\tPod" {
			found = true
		}
	}
	if !found {
		t.Errorf("kx <TAB> = %v..., want the resource types among the commands",
			candidates[:min(5, len(candidates))])
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// A spelling a command shadows is not offered as a kind: `kx secret` runs the
// secret command, so completing it as "list Secrets through get" describes
// something that will not happen.
func TestRootCompletionOmitsSpellingsCommandsShadow(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	candidates, _ := complete(t, root, "")

	for _, candidate := range candidates {
		spelling, _, _ := strings.Cut(candidate, "\t")
		switch spelling {
		case "secret", "secrets", "namespace", "ns":
			t.Errorf("kx <TAB> offered %q as a kind, but a command owns that word", candidate)
		}
	}
}

// Past the first word the line belongs to whichever command was named. If none
// was, kx has nothing to say about it — and files are still the wrong answer.
func TestRootCompletionStopsAfterTheFirstWord(t *testing.T) {
	root := NewRoot(completionServices(t), "test")

	candidates, directive := root.ValidArgsFunction(root, []string{"nonsense"}, "")
	if len(candidates) != 0 {
		t.Errorf("candidates = %v, want none past the first word", candidates)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// The commands and the kinds arrive as one list, and no word may appear in it
// twice — cobra adds its subcommand names to whatever the completer returns
// rather than choosing between them.
func TestRootCompletionMergesCommandsAndKindsWithoutDuplicates(t *testing.T) {
	root := NewRoot(completionServices(t), "test")
	var out bytes.Buffer
	root.SetOut(&out)

	if err := Execute(root, []string{cobra.ShellCompRequestCmd, ""}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	offered := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.HasPrefix(line, ":") {
			continue
		}
		spelling, _, _ := strings.Cut(line, "\t")
		offered[spelling]++
	}

	for _, want := range []string{"get", "port-forward", "secret", "pods", "po", "deploy"} {
		if offered[want] == 0 {
			t.Errorf("kx <TAB> did not offer %q", want)
		}
	}
	for spelling, count := range offered {
		if count > 1 {
			t.Errorf("kx <TAB> offered %q %d times, want once", spelling, count)
		}
	}
}

// Completion runs on every keystroke of a Tab, so it must survive a machine
// with no state file at all rather than erroring into the shell.
func TestCompletionWithoutSavedState(t *testing.T) {
	service := state.NewService(10)
	service.Path = filepath.Join(t.TempDir(), "missing.json")
	root := NewRoot(Services{State: service}, "test")

	candidates, directive := complete(t, root, "describe", "")
	if len(candidates) != 0 {
		t.Errorf("candidates = %v, want none without saved state", candidates)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}
