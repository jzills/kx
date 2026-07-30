package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

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
		{"get <resource> [kubectl flags]", []string{"resource:required"}},
		{"describe <index>... [kubectl flags]", []string{"index:required"}},
		{"secret [index]... [kubectl flags]", []string{"index:optional"}},
		{"scan [index] [scanner flags]", []string{"index:optional"}},
		{"top [kubectl flags]", nil},
		{"scale <index> <replicas>", []string{"index:required", "replicas:required"}},
		{"exec <index> [kubectl flags] [-- command...]",
			[]string{"index:required", "command:optional"}},
		{"label <index> [key=value...]", []string{"index:required", "key=value:optional"}},
		{"tree [index]", []string{"index:optional"}},
	}
	for _, tc := range cases {
		var got []string
		for _, arg := range positionalArgs(tc.use) {
			got = append(got, arg.Name+":"+arg.Doc)
		}
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("positionalArgs(%q) = %v, want %v", tc.use, got, tc.want)
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
		// cobra adds these itself; they aren't part of kx's surface.
		if name == "help" || name == "completion" {
			continue
		}
		if !listed[name] {
			t.Errorf("command %q is not in any help section", name)
		}
	}
}
