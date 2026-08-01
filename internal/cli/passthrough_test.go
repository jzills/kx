package cli

import (
	"strings"
	"testing"
)

func joined(args []string) string { return strings.Join(args, " ") }

func TestExtractStringForms(t *testing.T) {
	cases := map[string]struct {
		args      []string
		wantValue string
		wantRest  string
	}{
		"long with space":    {[]string{"pods", "--match", "web"}, "web", "pods"},
		"long with equals":   {[]string{"pods", "--match=web"}, "web", "pods"},
		"short with space":   {[]string{"pods", "-m", "web"}, "web", "pods"},
		"short with equals":  {[]string{"pods", "-m=web"}, "web", "pods"},
		"absent":             {[]string{"pods", "-n", "prod"}, "", "pods -n prod"},
		"last wins":          {[]string{"pods", "-m", "a", "-m", "b"}, "b", "pods"},
		"attached shorthand": {[]string{"pods", "-mweb"}, "web", "pods"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			value, rest, err := extractString(tc.args, "--match", "-m")
			if err != nil {
				t.Fatalf("extractString: %v", err)
			}
			if value != tc.wantValue {
				t.Errorf("value = %q, want %q", value, tc.wantValue)
			}
			if joined(rest) != tc.wantRest {
				t.Errorf("rest = %q, want %q", joined(rest), tc.wantRest)
			}
		})
	}
}

// Regression test for the reported bug: `kx scan -ndiagnostics` swept the
// wrong namespace because the attached-shorthand spelling fell through to
// `rest` uncaught. Exercised with the real --namespace/-n pair rather than
// --match/-m so the fix is proven against the exact flags scan.go uses.
func TestExtractStringNamespaceAttachedShorthand(t *testing.T) {
	value, rest, err := extractString([]string{"-ndiagnostics"}, "--namespace", "-n")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "diagnostics" {
		t.Errorf("value = %q, want diagnostics", value)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %q, want empty", joined(rest))
	}
}

// Regression guard on the len(arg) > len(short) condition: a bare "-n" must
// still take the following argument rather than being swallowed as an
// attached value trimmed down to "".
func TestExtractStringBareShortStillTakesNextArg(t *testing.T) {
	value, rest, err := extractString([]string{"-n", "diagnostics"}, "--namespace", "-n")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "diagnostics" {
		t.Errorf("value = %q, want diagnostics", value)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %q, want empty", joined(rest))
	}
}

func TestExtractStringMissingValue(t *testing.T) {
	if _, _, err := extractString([]string{"pods", "-m"}, "--match", "-m"); err == nil {
		t.Error("extractString accepted a flag with no value")
	}
}

// The whole point of the helper: everything kx doesn't own reaches kubectl
// untouched, in its original order.
func TestExtractStringPreservesKubectlFlags(t *testing.T) {
	args := []string{"pods", "-n", "prod", "-l", "app=web", "--sort-by=.metadata.name", "-o", "wide"}
	value, rest, err := extractString(args, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "" {
		t.Errorf("value = %q, want empty", value)
	}
	if joined(rest) != joined(args) {
		t.Errorf("rest = %q, want it unchanged", joined(rest))
	}
}

func TestExtractStringKeepsKubectlFlagsAlongsideMatch(t *testing.T) {
	args := []string{"pods", "-n", "prod", "-m", "web", "-l", "app=api"}
	value, rest, err := extractString(args, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "web" {
		t.Errorf("value = %q, want web", value)
	}
	if want := "pods -n prod -l app=api"; joined(rest) != want {
		t.Errorf("rest = %q, want %q", joined(rest), want)
	}
}

// hasFlag must recognise exactly the spellings extractString consumes. If it
// falls behind — missing attached shorthand, say — a caller that checks
// presence before extracting (scan's --namespace/--all-namespaces guard) sees
// "absent" for a flag extractString is quietly consuming anyway.
func TestHasFlag(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"long":               {[]string{"pods", "--namespace", "prod"}, true},
		"long with equals":   {[]string{"pods", "--namespace=prod"}, true},
		"short with space":   {[]string{"pods", "-n", "prod"}, true},
		"short with equals":  {[]string{"pods", "-n=prod"}, true},
		"attached shorthand": {[]string{"pods", "-nprod"}, true},
		"absent":             {[]string{"pods", "-l", "app=web"}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hasFlag(tc.args, "--namespace", "-n"); got != tc.want {
				t.Errorf("hasFlag = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractBool(t *testing.T) {
	present, rest := extractBool([]string{"pods", "--no-color", "-n", "prod"}, "--no-color")
	if !present {
		t.Error("present = false, want true")
	}
	if want := "pods -n prod"; joined(rest) != want {
		t.Errorf("rest = %q, want %q", joined(rest), want)
	}

	if present, _ := extractBool([]string{"pods"}, "--no-color"); present {
		t.Error("present = true for absent flag")
	}
}

// "<name>=<value>" forms, covering both the long and short spellings of a
// flag with aliases (extractBool's variadic names). "=false" is the one case
// where a matched token must still be removed from rest without making the
// flag present.
func TestExtractBoolForms(t *testing.T) {
	cases := map[string]struct {
		args     []string
		want     bool
		wantRest string
	}{
		"bare long":         {[]string{"pods", "--all-namespaces", "-n", "prod"}, true, "pods -n prod"},
		"long equals true":  {[]string{"pods", "--all-namespaces=true", "-n", "prod"}, true, "pods -n prod"},
		"long equals false": {[]string{"pods", "--all-namespaces=false", "-n", "prod"}, false, "pods -n prod"},
		"short equals true": {[]string{"pods", "-A=true", "-n", "prod"}, true, "pods -n prod"},
		"unparseable value": {[]string{"pods", "--all-namespaces=banana"}, true, "pods"},
		"absent":            {[]string{"pods", "-n", "prod"}, false, "pods -n prod"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			present, rest := extractBool(tc.args, "--all-namespaces", "-A")
			if present != tc.want {
				t.Errorf("present = %v, want %v", present, tc.want)
			}
			if joined(rest) != tc.wantRest {
				t.Errorf("rest = %q, want %q", joined(rest), tc.wantRest)
			}
		})
	}
}

// A kubectl value that happens to equal a kx flag name is still consumed as
// kx's flag. Documenting the known limit: `--` style separation would be the
// fix if this ever bites, and it matches what the Python implementation does
// today, since Click also matches on token equality.
func TestExtractStringConsumesFlagLikeValue(t *testing.T) {
	value, rest, err := extractString([]string{"pods", "-m", "-n"}, "--match", "-m")
	if err != nil {
		t.Fatalf("extractString: %v", err)
	}
	if value != "-n" {
		t.Errorf("value = %q, want -n", value)
	}
	if joined(rest) != "pods" {
		t.Errorf("rest = %q, want pods", joined(rest))
	}
}
