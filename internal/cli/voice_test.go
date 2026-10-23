package cli

import (
	"strings"
	"testing"
	"unicode"
)

// Cobra and pflag refuse a typo before any of kx's own code runs, so these go
// through Execute with real argv: the root's flag-error function and the
// rewrite of cobra's unknown-command message are only wired there. Each case
// pins the whole sentence, so a regression to cobra's wording fails by name
// rather than by a missing substring.
func TestCobraRefusalsSpeakInKxsVoice(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"frobnicate"}, "'frobnicate' isn't a kx command — see 'kx --help' for the list."},
		{[]string{"mcp", "x"}, "'x' isn't an argument of kx mcp — see 'kx mcp --help' for usage."},
		{[]string{"theme", "a", "b"}, "'b' isn't an argument of kx theme — see 'kx theme --help' for usage."},
		{[]string{"state", "--bogus"}, "'--bogus' isn't a flag of kx state — see 'kx state --help' for usage."},
		{[]string{"state", "-Z"}, "'-Z' isn't a flag of kx state — see 'kx state --help' for usage."},
		{[]string{"state", "--all=x"}, "Invalid value for '--all': 'x' is not a valid bool."},
		{[]string{"tree", "-n"}, "'-n' needs a value."},
		// A forwarding command's flagError hands everything but an unknown
		// flag back to the root's, so its missing value reads the same.
		{[]string{"get", "pods", "-n"}, "'-n' needs a value."},
		{[]string{"state", "---x"}, "'---x' isn't a flag — a flag starts with one dash or two."},
	} {
		quietRender(t)
		err := Execute(NewRoot(argvServices(t), "test"), tc.argv)
		if err == nil {
			t.Errorf("kx %s was accepted", strings.Join(tc.argv, " "))
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("kx %s: err = %q, want %q", strings.Join(tc.argv, " "), err, tc.want)
		}
	}
}

// Suggestions are cobra's to compute, so the case asserts their shape — the
// quoted names offered in a question — rather than which names a distance
// metric picks. TestCobraRefusalsSpeakInKxsVoice's "frobnicate" covers the
// branch with none.
func TestUnknownCommandSpeaksInKxsVoice(t *testing.T) {
	quietRender(t)
	err := Execute(NewRoot(argvServices(t), "test"), []string{"gett"})
	if err == nil {
		t.Fatal("kx gett was accepted")
	}
	got := err.Error()
	if !strings.HasPrefix(got, "'gett' isn't a kx command — did you mean ") ||
		!strings.Contains(got, "'get'") || !strings.HasSuffix(got, "?") {
		t.Errorf("err = %q, want a kx-voiced refusal offering 'get'", got)
	}
	// Proves the rewrite matched rather than cobra's message happening to
	// contain the same words: cobra's own carries its layout.
	if strings.Contains(got, "Did you mean this?") || strings.Contains(got, "\n") {
		t.Errorf("err = %q, still carries cobra's suggestion block", got)
	}
}

func TestUnknownCommandListsSeveralSuggestions(t *testing.T) {
	root := NewRoot(argvServices(t), "test")
	err := unknownCommandError(root, "x", []string{"a", "b", "c"})
	if want := "'x' isn't a kx command — did you mean 'a', 'b' or 'c'?"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// Every refusal above opens like a sentence and closes like one; cobra's own
// do neither, so this is the property a new cobra message slipping through
// would break first.
func TestCobraRefusalsAreSentences(t *testing.T) {
	for _, argv := range [][]string{
		{"gett"}, {"mcp", "x"}, {"state", "--bogus"}, {"tree", "-n"}, {"state", "--all=x"},
	} {
		quietRender(t)
		err := Execute(NewRoot(argvServices(t), "test"), argv)
		if err == nil {
			t.Errorf("kx %s was accepted", strings.Join(argv, " "))
			continue
		}
		msg := err.Error()
		first := []rune(msg)[0]
		if first != '\'' && !unicode.IsUpper(first) {
			t.Errorf("kx %s: %q opens lowercase", strings.Join(argv, " "), msg)
		}
		if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, "?") {
			t.Errorf("kx %s: %q has no closing punctuation", strings.Join(argv, " "), msg)
		}
	}
}
