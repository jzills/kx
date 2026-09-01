package cli

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rootHelpText renders the top-level help screen for a build at the given
// version, which is the only way to see what the screen actually closes with:
// the footer and the version line are composed on the way to the renderer.
func rootHelpText(t *testing.T, version string) string {
	t.Helper()
	var out bytes.Buffer
	render.SetOutput(&out, &out, config.DefaultTheme)
	t.Cleanup(func() { render.SetOutput(nil, nil, config.DefaultTheme) })

	root := NewRoot(Services{}, version)
	if err := root.Help(); err != nil {
		t.Fatalf("root.Help(): %v", err)
	}
	return out.String()
}

// The theme listing numbers its rows, so typing the number is the obvious
// thing to try.
func TestResolveThemeByIndex(t *testing.T) {
	name, err := resolveTheme("1")
	if err != nil {
		t.Fatalf("resolveTheme: %v", err)
	}
	if name != "github-dark" {
		t.Errorf("resolveTheme(\"1\") = %q, want github-dark", name)
	}
}

func TestResolveThemeByName(t *testing.T) {
	name, err := resolveTheme("dracula")
	if err != nil {
		t.Fatalf("resolveTheme: %v", err)
	}
	if name != "dracula" {
		t.Errorf("resolveTheme = %q, want dracula", name)
	}
}

func TestResolveThemeOutOfRange(t *testing.T) {
	for _, argument := range []string{"0", "99", "-1"} {
		_, err := resolveTheme(argument)
		if err == nil {
			t.Errorf("resolveTheme(%q) succeeded, want an error", argument)
			continue
		}
		if !strings.Contains(err.Error(), "out of range") {
			t.Errorf("resolveTheme(%q) error = %q, want an out-of-range message", argument, err)
		}
	}
}

func TestResolveThemeUnknownName(t *testing.T) {
	_, err := resolveTheme("nonexistent")
	if err == nil || !strings.Contains(err.Error(), "Unknown theme") {
		t.Errorf("error = %v, want an unknown-theme message", err)
	}
}

func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "kx"}
	root.AddCommand(&cobra.Command{Use: "get"})
	// `ns` is a registered alias, not a kind spelling, and must keep its
	// namespace-switch meaning.
	namespace := &cobra.Command{Use: "namespace", Aliases: []string{"ns"}}
	root.AddCommand(namespace)
	return root
}

// `kx pods` means `kx get pods`.
func TestKindAliasRewritesToGet(t *testing.T) {
	for _, spelling := range []string{"pods", "po", "deploy", "svc"} {
		got := rewriteKindAlias(testRoot(), []string{spelling, "-n", "prod"})
		want := "get " + spelling + " -n prod"
		if strings.Join(got, " ") != want {
			t.Errorf("rewrite(%q) = %q, want %q", spelling, strings.Join(got, " "), want)
		}
	}
}

// Registered commands always win: `kx ns 3` switches namespace rather than
// listing namespaces via get.
func TestKindAliasDoesNotShadowCommands(t *testing.T) {
	for _, args := range [][]string{{"ns", "3"}, {"namespace"}, {"get", "pods"}} {
		got := rewriteKindAlias(testRoot(), args)
		if strings.Join(got, " ") != strings.Join(args, " ") {
			t.Errorf("rewrite(%v) = %v, want it unchanged", args, got)
		}
	}
}

func TestKindAliasLeavesFlagsAndUnknownWords(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-v"}, {"nonsense"}, {}} {
		got := rewriteKindAlias(testRoot(), args)
		if strings.Join(got, " ") != strings.Join(args, " ") {
			t.Errorf("rewrite(%v) = %v, want it unchanged", args, got)
		}
	}
}

// `kx po <TAB>` reaches kx as `kx __complete po ""`: the words being completed
// are arguments to cobra's own command, so the alias has to be applied to them
// too or completion answers for a command that does not exist.
func TestCompletionRequestRewritesTheLineBeingCompleted(t *testing.T) {
	for _, request := range []string{
		cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd,
	} {
		for _, args := range [][]string{
			{request, "po", ""},           // kx po <TAB>
			{request, "po", "1", ""},      // kx po 1 <TAB>
			{request, "deploy", "-n", ""}, // kx deploy -n <TAB>
		} {
			got := rewriteArgs(testRoot(), args)
			want := append([]string{request, "get"}, args[1:]...)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("rewrite(%v) = %v, want %v", args, got, want)
			}
		}
	}
}

// The last word of a completion request is the one being completed, so it is a
// fragment rather than a spelling. `kx po<TAB>` is asking which commands start
// with "po" and must keep answering port-forward.
func TestCompletionRequestLeavesTheWordBeingCompleted(t *testing.T) {
	for _, args := range [][]string{
		{cobra.ShellCompRequestCmd, "po"},
		{cobra.ShellCompRequestCmd, ""},
		{cobra.ShellCompRequestCmd},
		{cobra.ShellCompRequestCmd, "ns", "3", ""}, // a command still wins
		{cobra.ShellCompRequestCmd, "get", "pods", ""},
	} {
		got := rewriteArgs(testRoot(), args)
		if strings.Join(got, " ") != strings.Join(args, " ") {
			t.Errorf("rewrite(%v) = %v, want it unchanged", args, got)
		}
	}
}

// A plain command line keeps being rewritten whole: the word at the end of one
// is an argument, not something half-typed.
func TestRewriteArgsLeavesPlainCommandLinesToTheAlias(t *testing.T) {
	for _, args := range [][]string{{"pods"}, {"po", "-n", "prod"}, {"get", "pods"}} {
		got := rewriteArgs(testRoot(), args)
		want := rewriteKindAlias(testRoot(), args)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("rewriteArgs(%v) = %v, want %v", args, got, want)
		}
	}
}

// Every command on the root help screen must be listed in a section, or it is
// invisible to anyone reading `kx --help`.
// The Use string is the only description of a command's positional arguments
// cobra keeps, so the help screen's Arguments section is parsed back out of it.
// Bracket and ellipsis punctuation is part of that spec, not part of an
// argument's name, and a two-word placeholder documents flag pass-through
// rather than an argument the user names.
func TestPositionalArgsReadsTheUseSpec(t *testing.T) {
	cases := []struct {
		use  string
		want []string
	}{
		{"get <resource> [kubectl flags]", []string{"<resource>"}},
		{"describe <index>... [kubectl flags]", []string{"<index>..."}},
		{"secret [index]... [kubectl flags]", []string{"[index]..."}},
		{"scan [index] [scanner flags]", []string{"[index]"}},
		{"top [kubectl flags]", nil},
		{"scale <index> <replicas>", []string{"<index>", "<replicas>"}},
		{"exec <index> [kubectl flags] [-- command...]",
			[]string{"<index>", "[command]..."}},
		{"label <index> [key=value...]", []string{"<index>", "[key=value]..."}},
		{"tree [index]", []string{"[index]"}},
	}
	for _, tc := range cases {
		var got []string
		for _, arg := range positionalArgs(&cobra.Command{Use: tc.use}) {
			got = append(got, arg.Name)
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("positionalArgs(%q) = %v, want %v", tc.use, got, tc.want)
		}
	}
}

// requiredArgsError is generated from a command's own Use string, so its
// wording is exercised here directly rather than through a full command tree
// — only the required prefix is named, an optional trailing argument (exec's
// command, get's index) is left out because it isn't what's missing.
func TestRequiredArgsErrorNamesOnlyTheRequiredPrefix(t *testing.T) {
	cases := []struct {
		use  string
		want string
	}{
		{"describe <index>... [kubectl flags]", "kx describe requires <index>... — see 'kx describe --help' for usage."},
		{"get <resource> [index]... [kubectl flags]", "kx get requires <resource> — see 'kx get --help' for usage."},
		{"scale <index> <replicas>", "kx scale requires <index> <replicas> — see 'kx scale --help' for usage."},
		{"rollout <action> <index>", "kx rollout requires <action> <index> — see 'kx rollout --help' for usage."},
		{"exec <index> [kubectl flags] [-- command...]", "kx exec requires <index> — see 'kx exec --help' for usage."},
		{"label <index> [key=value...]", "kx label requires <index> — see 'kx label --help' for usage."},
	}
	for _, tc := range cases {
		// CommandPath climbs the parent chain, so a bare command reports just
		// its own name ("describe") rather than "kx describe" — give it the
		// same root every registered command actually has.
		root := &cobra.Command{Use: "kx"}
		cmd := &cobra.Command{Use: tc.use}
		root.AddCommand(cmd)

		got := requiredArgsError(cmd).Error()
		if got != tc.want {
			t.Errorf("requiredArgsError(%q) = %q, want %q", tc.use, got, tc.want)
		}
	}
}

// minArgs and exactArgs must actually gate RunE, not just build a nicer
// message nobody sees: a satisfied count returns nil, an unsatisfied one
// returns the generated error.
func TestMinArgsAndExactArgsGateOnCount(t *testing.T) {
	cmd := &cobra.Command{Use: "describe <index>..."}
	validate := minArgs(1)
	if err := validate(cmd, []string{"1"}); err != nil {
		t.Errorf("minArgs(1) with one arg: %v, want nil", err)
	}
	if err := validate(cmd, nil); err == nil {
		t.Error("minArgs(1) with zero args returned nil, want an error")
	}

	scale := &cobra.Command{Use: "scale <index> <replicas>"}
	exact := exactArgs(2)
	if err := exact(scale, []string{"1", "3"}); err != nil {
		t.Errorf("exactArgs(2) with two args: %v, want nil", err)
	}
	for _, args := range [][]string{nil, {"1"}, {"1", "3", "extra"}} {
		if err := exact(scale, args); err == nil {
			t.Errorf("exactArgs(2) with %v returned nil, want an error", args)
		}
	}
}

// Every command that requires a fixed or minimum number of arguments must
// answer a shortfall in kx's own voice, generated from its own Use string —
// not cobra's "requires at least N arg(s), only received M". Explicit rather
// than discovered from the tree, so a command quietly reverting to
// cobra.MinimumNArgs/ExactArgs directly is a failing assertion here instead
// of a silent regression back to cobra's wording.
func TestMissingRequiredArgsSpeakInKxsVoiceNotCobras(t *testing.T) {
	root := NewRoot(Services{}, "test")
	for _, name := range []string{
		"describe", "logs", "edit", "exec", "debug", "delete", "scale",
		"rollout", "yaml", "labels", "annotations", "label", "annotate",
		"events", "cordon", "uncordon", "get",
	} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("root.Find(%q): %v", name, err)
		}
		if cmd.Args == nil {
			t.Fatalf("%q has no Args validator", name)
		}
		got := cmd.Args(cmd, nil)
		if got == nil {
			t.Fatalf("kx %s accepted zero arguments", name)
		}
		want := "kx " + name + " requires "
		if !strings.HasPrefix(got.Error(), want) {
			t.Errorf("kx %s error = %q, want it to start with %q", name, got.Error(), want)
		}
		if strings.Contains(got.Error(), "arg(s)") {
			t.Errorf("kx %s error = %q, still speaks in cobra's voice", name, got.Error())
		}
	}
}

// The README's command table marks repeatable arguments, and the ellipsis sits
// either inside the brackets or after them depending on the spelling — both
// mean the same thing, and missing either understates the command.
func TestParseUseDetectsRepeatableArgsAndPassthrough(t *testing.T) {
	cases := []struct {
		use         string
		variadic    []bool
		passthrough string
	}{
		{"describe <index>... [kubectl flags]", []bool{true}, "kubectl flags"},
		{"secret [index]... [kubectl flags]", []bool{true}, "kubectl flags"},
		{"label <index> [key=value...]", []bool{false, true}, ""},
		{"exec <index> [kubectl flags] [-- command...]", []bool{false, true}, "kubectl flags"},
		{"scan [index] [scanner flags]", []bool{false}, "scanner flags"},
		{"scale <index> <replicas>", []bool{false, false}, ""},
	}
	for _, tc := range cases {
		spec := ParseUse(tc.use)
		if len(spec.Args) != len(tc.variadic) {
			t.Errorf("ParseUse(%q) found %d args, want %d", tc.use, len(spec.Args), len(tc.variadic))
			continue
		}
		for i, want := range tc.variadic {
			if spec.Args[i].Variadic != want {
				t.Errorf("ParseUse(%q) arg %q variadic = %v, want %v",
					tc.use, spec.Args[i].Name, spec.Args[i].Variadic, want)
			}
		}
		if spec.Passthrough != tc.passthrough {
			t.Errorf("ParseUse(%q) passthrough = %q, want %q",
				tc.use, spec.Passthrough, tc.passthrough)
		}
	}
}

// Command names inside a section are alphabetized so the listing is scannable
// without memorizing an editorial order; section titles keep their own order.
func TestHelpSectionCommandsAreAlphabetized(t *testing.T) {
	for _, section := range helpSections {
		for i := 1; i < len(section.Commands); i++ {
			if section.Commands[i-1] > section.Commands[i] {
				t.Errorf("section %q: %q comes before %q, not alphabetical",
					section.Title, section.Commands[i-1], section.Commands[i])
			}
		}
	}
}

// Any command whose Use string passes through flags (describe, logs, edit,
// exec, port-forward, cp, get, secret, scan) gets the note for free from its
// Use string, rather than a hand-written sentence that can drift out of sync
// per command.
func TestCommandHelpNotesKubectlPassthrough(t *testing.T) {
	passthroughNote := "Unrecognized flags are passed through to kubectl."

	withPassthrough := &cobra.Command{
		Use:   "describe <index>... [kubectl flags]",
		Short: "Show full kubectl describe output.",
	}
	if !strings.Contains(commandHelp(withPassthrough).Doc, passthroughNote) {
		t.Errorf("commandHelp(%q).Doc = %q, want it to contain %q",
			withPassthrough.Use, commandHelp(withPassthrough).Doc, passthroughNote)
	}

	withoutPassthrough := &cobra.Command{
		Use:   "scale <index> <replicas>",
		Short: "Scale an indexed workload.",
	}
	if strings.Contains(commandHelp(withoutPassthrough).Doc, passthroughNote) {
		t.Errorf("commandHelp(%q).Doc = %q, want no passthrough note",
			withoutPassthrough.Use, commandHelp(withoutPassthrough).Doc)
	}
}

// state gained subcommands in this change; its --help must list them so
// they're not registered-but-undiscoverable.
func TestStateHelpListsItsSubcommands(t *testing.T) {
	root := NewRoot(Services{}, "test")
	stateCmd, _, err := root.Find([]string{"state"})
	if err != nil {
		t.Fatalf("root.Find(state): %v", err)
	}
	help := commandHelp(stateCmd)
	var names []string
	for _, item := range help.Commands {
		names = append(names, item.Name)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"back", "drop", "forward"} {
		if !strings.Contains(joined, want) {
			t.Errorf("kx state --help Commands = %q, missing %q", joined, want)
		}
	}
}

// --watch is parsed by hand (isWatch) rather than by cobra, so nothing forces
// it to be registered — and for a while it wasn't, leaving a flag kx gives its
// own live-table behaviour documented only in the README. Both listing
// commands run through runGet, so both honour it and both must show it.
func TestListingCommandsDocumentWatch(t *testing.T) {
	root := NewRoot(Services{}, "test")
	for _, name := range []string{"get", "secret"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("root.Find(%s): %v", name, err)
		}
		var options []string
		for _, option := range commandHelp(cmd).Options {
			options = append(options, option.Name)
		}
		joined := strings.Join(options, " ")
		if !strings.Contains(joined, "--watch") {
			t.Errorf("kx %s --help Options = %q, missing --watch", name, joined)
		}
	}
}

func TestEveryCommandAppearsInAHelpSection(t *testing.T) {
	listed := map[string]bool{}
	for _, section := range helpSections {
		for _, name := range section.Commands {
			listed[name] = true
		}
	}

	root := NewRoot(Services{}, "test")
	for _, cmd := range root.Commands() {
		name := cmd.Name()
		// `help` is cobra's and isn't part of kx's surface. Hidden commands are
		// the pre-restructure kx back/forward/drop spellings, deliberately
		// absent from --help now that kx state back/forward/drop are
		// canonical — see NewRoot.
		if name == "help" || cmd.Hidden {
			continue
		}
		if !listed[name] {
			t.Errorf("command %q is not in any help section", name)
		}
	}
}

// The completion command works whether or not it is advertised, so nothing
// failed while it was invisible: cobra built it during Execute, after the help
// screen and the README generator had already read the command tree.
func TestCompletionAppearsOnTheRootScreen(t *testing.T) {
	root := NewRoot(Services{}, "test")
	if _, _, err := root.Find([]string{"completion"}); err != nil {
		t.Fatalf("root.Find(completion): %v", err)
	}

	for _, section := range rootSections(root) {
		for _, item := range section.Items {
			if item.Name == "completion" {
				if item.Doc == "" {
					t.Error("completion is listed with no description")
				}
				return
			}
		}
	}
	t.Error("completion is registered but absent from every root help section")
}

// The front page teaches the index workflow by example, so a renamed or
// removed command would leave it demonstrating a spelling kx no longer
// accepts.
func TestExampleBlockUsesRealSpellings(t *testing.T) {
	root := NewRoot(Services{}, "test")
	ranges := false

	for _, item := range examples {
		fields := strings.Fields(item.Name)
		if len(fields) < 2 || fields[0] != "kx" {
			t.Errorf("example %q does not start with 'kx <something>'", item.Name)
			continue
		}
		// Whatever follows `kx` must resolve the way Execute resolves it:
		// a registered command, or a kind spelling rewritten to `kx get`.
		verb := fields[1]
		cmd, _, err := root.Find([]string{verb})
		if (err != nil || cmd == root) && !kinds.IsKindSpelling(verb) {
			t.Errorf("example %q: %q is neither a command nor a kind", item.Name, verb)
		}
		// Only the spelling column counts: an ellipsis in a description ("rows
		// 1, 2, 3...") contains ".." without demonstrating a range.
		if strings.Contains(item.Name, "..") {
			ranges = true
		}
	}

	if !ranges {
		t.Error("selecting block never mentions range syntax, which exists nowhere else in --help")
	}
}

// The options block is derived from the registered flags rather than written
// out beside them; the copy it replaced described --version with wording the
// flag itself had never carried.
func TestRootOptionsMirrorTheRegisteredFlags(t *testing.T) {
	root := NewRoot(Services{}, "test")
	shown := map[string]string{}
	for _, option := range rootOptions(root) {
		shown[option.Name] = option.Doc
	}

	root.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Hidden {
			return
		}
		doc, ok := shown[flagNames(flag)]
		if !ok {
			t.Errorf("flag %q is registered on kx but missing from the root Options block", flag.Name)
			return
		}
		if doc != flag.Usage {
			t.Errorf("flag %q: root help says %q, the flag says %q", flag.Name, doc, flag.Usage)
		}
	})

	if _, ok := shown["-h, --help"]; !ok {
		t.Error("root Options block omits -h, --help")
	}
}

// Configuration lived only in the README: nothing in the binary named the file
// kx reads, the file it writes, or a single environment override.
func TestRootHelpDocumentsFilesAndEnvironment(t *testing.T) {
	names := []string{}
	for _, item := range files() {
		names = append(names, item.Name)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"config.toml", "state.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Files block = %q, missing %s", joined, want)
		}
	}

	documented := map[string]bool{}
	for _, item := range environment() {
		documented[item.Name] = true
		if item.Doc == "" {
			t.Errorf("environment variable %s is listed with no description", item.Name)
		}
	}
	for _, setting := range config.Settings() {
		if !documented[setting.Env] {
			t.Errorf("config override %s is missing from the Environment block", setting.Env)
		}
	}
	// The block is kx's own settings and nothing else. NO_COLOR was listed
	// here once: kx honors it, but it is a terminal-wide convention rather
	// than something kx defines, and a reader scanning this list for what kx
	// can be told to do shouldn't have to sort the two apart.
	for name := range documented {
		if !strings.HasPrefix(name, "KX_") {
			t.Errorf("Environment block lists %s, which is not a kx setting", name)
		}
	}
}

// TestHelpSectionCommandsAreAlphabetized applies the same rule to the command
// sections, where the order is written down in helpSections. Here it isn't:
// the Environment block is built from config.Settings(), whose order is
// whatever the loader's author found convenient.
func TestEnvironmentBlockIsAlphabetical(t *testing.T) {
	names := []string{}
	for _, item := range environment() {
		names = append(names, item.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("Environment block is out of order: %v", names)
	}
}

// The help screen names the kx you are running, not the commit it was built
// from. An untagged build's version carries a timestamp and a commit hash;
// under --help that is a line of noise where a reader wanted one fact, and
// `kx --version` reports the whole thing a keystroke away.
func TestRootHelpSignsOffWithTheShortVersion(t *testing.T) {
	text := rootHelpText(t, "0.3.4-0.20260813184656-8ec57390b220")

	if !strings.Contains(text, "v0.3.4") {
		t.Errorf("root help =\n%s\nmissing the version it was built at", text)
	}
	if strings.Contains(text, "20260813184656") || strings.Contains(text, "8ec57390b220") {
		t.Errorf("root help =\n%s\nstill carries the pseudo-version's build tail", text)
	}
}

// The docs URL closed this screen and appears under --version as well. One
// copy is enough, and the footer's job is the next command to run.
func TestRootHelpFooterCarriesNoURL(t *testing.T) {
	text := rootHelpText(t, "0.3.2")

	if !strings.Contains(text, "kx COMMAND --help") {
		t.Errorf("root help =\n%s\nlost the footer's pointer at per-command help", text)
	}
	// The same const --version prints, so the two screens can't drift apart on
	// which of them carries the link.
	if strings.Contains(text, docsURL) {
		t.Errorf("root help =\n%s\nstill prints %s; --version is where it lives", text, docsURL)
	}
}

// The Arguments block used to read "required" or "optional" and nothing else,
// which the Usage line above it already showed. Every argument any command
// declares must now say what it is — a new command with a new argument name
// fails here until argDocs describes it.
func TestEveryArgumentIsDocumented(t *testing.T) {
	root := NewRoot(Services{}, "test")

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, arg := range ParseUse(cmd.Use).Args {
			if doc := argDoc(cmd, arg.Name); doc == "" {
				t.Errorf("%s takes %q, but nothing describes it", cmd.CommandPath(), arg.Name)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// A flag that takes a value reads no differently from a switch unless the help
// says so, and the two spellings of a flag's name were rendered three
// different ways across the screens.
func TestOptionsShowShorthandFirstAndValueType(t *testing.T) {
	root := NewRoot(Services{}, "test")
	cases := []struct{ command, flag, want string }{
		{"diagnostic", "namespace", "-n, --namespace string"},
		{"diagnostic", "port", "--port int"},
		{"diagnostic", "all-namespaces", "-A, --all-namespaces"},
		{"label", "remove", "--remove strings"},
	}
	for _, tc := range cases {
		cmd, _, err := root.Find([]string{tc.command})
		if err != nil {
			t.Fatalf("root.Find(%s): %v", tc.command, err)
		}
		flag := cmd.Flags().Lookup(tc.flag)
		if flag == nil {
			t.Fatalf("kx %s has no --%s", tc.command, tc.flag)
		}
		if got := flagSpelling(flag); got != tc.want {
			t.Errorf("kx %s --%s renders as %q, want %q", tc.command, tc.flag, got, tc.want)
		}
	}
}

// --no-color and --help apply to every command and say nothing about the one
// being read, so they sit in their own block rather than padding out the list
// of flags that are actually the command's.
func TestGlobalFlagsAreSeparatedFromTheCommandsOwn(t *testing.T) {
	root := NewRoot(Services{}, "test")
	cmd, _, err := root.Find([]string{"tree"})
	if err != nil {
		t.Fatalf("root.Find(tree): %v", err)
	}
	help := commandHelp(cmd)

	var own, global []string
	for _, option := range help.Options {
		own = append(own, option.Name)
	}
	for _, option := range help.Global {
		global = append(global, option.Name)
	}

	for _, name := range own {
		if strings.Contains(name, "--no-color") {
			t.Errorf("--no-color is listed among kx tree's own options: %v", own)
		}
	}
	joined := strings.Join(global, " ")
	for _, want := range []string{"--no-color", "-h, --help"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Global options = %v, missing %s", global, want)
		}
	}
	if len(own) == 0 {
		t.Error("kx tree's own options are empty; the split swallowed them")
	}
}

// SuggestFor covers the spellings cobra's own edit-distance rule misses:
// `kx rm 3` is not a typo for delete, it is a different tool's word for it.
//
// A value cobra would already suggest must not be listed, or the command is
// appended twice and the prompt reads "Did you mean this? logs logs".
func TestSuggestForAddsWhatEditDistanceMisses(t *testing.T) {
	root := NewRoot(Services{}, "test")

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, value := range cmd.SuggestFor {
			occurrences := 0
			for _, suggestion := range root.SuggestionsFor(value) {
				if suggestion == cmd.Name() {
					occurrences++
				}
			}
			switch occurrences {
			case 1:
			case 0:
				t.Errorf("kx %s lists SuggestFor %q, which suggests nothing", cmd.Name(), value)
			default:
				t.Errorf("kx %s lists SuggestFor %q, which cobra already suggests — %s would be offered %d times",
					cmd.Name(), value, cmd.Name(), occurrences)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// A suggestion for a spelling that already runs something is never seen, and
// would be wrong if it were: `kx forward` is a real command, so offering
// port-forward for it would contradict what the word does.
func TestSuggestForDoesNotShadowRealSpellings(t *testing.T) {
	root := NewRoot(Services{}, "test")

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, value := range cmd.SuggestFor {
			if found, _, err := root.Find([]string{value}); err == nil && found != root {
				t.Errorf("kx %s suggests %q, but %q already runs kx %s",
					cmd.Name(), value, value, found.Name())
			}
			if kinds.IsKindSpelling(value) {
				t.Errorf("kx %s suggests %q, but %q is a kind spelling and runs kx get %s",
					cmd.Name(), value, value, value)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// How indexes resolve is the one concept no command's own behaviour reveals,
// and it lived only in the README: that the history is a stack with a cursor,
// and that namespaces and contexts sit in slots outside it — which is why
// `kx ns 2` survives any number of `kx get` runs when no other index does.
func TestStateAndSwitchHelpExplainTheSlots(t *testing.T) {
	root := NewRoot(Services{}, "test")

	cases := []struct {
		command string
		phrases []string
	}{
		// `kx ns 2` is the claim itself, not the preamble to it: an index that
		// survives intervening listings, which nothing else in kx does.
		{"state", []string{"cursor", "max_history", "slots of their own", "kx ns 2", "--targets", "kx get ns"}},
		{"namespace", []string{"slot of its own", "kx get ns"}},
		{"context", []string{"slot of its own"}},
	}
	for _, tc := range cases {
		cmd, _, err := root.Find([]string{tc.command})
		if err != nil {
			t.Fatalf("root.Find(%s): %v", tc.command, err)
		}
		doc := commandHelp(cmd).Doc
		for _, phrase := range tc.phrases {
			if !strings.Contains(doc, phrase) {
				t.Errorf("kx %s --help does not mention %q:\n%s", tc.command, phrase, doc)
			}
		}
	}
}

// kx context has no `kx get` equivalent to route people to, so the paragraph
// that sends namespace users there must not appear on it.
func TestContextHelpOmitsTheNamespaceOnlyAdvice(t *testing.T) {
	root := NewRoot(Services{}, "test")
	cmd, _, err := root.Find([]string{"context"})
	if err != nil {
		t.Fatalf("root.Find(context): %v", err)
	}
	if strings.Contains(commandHelp(cmd).Doc, "kx get ns") {
		t.Error("kx context --help points at kx get ns, which lists namespaces, not contexts")
	}
}
