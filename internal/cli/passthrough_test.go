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
		"long with space":   {[]string{"pods", "--match", "web"}, "web", "pods"},
		"long with equals":  {[]string{"pods", "--match=web"}, "web", "pods"},
		"short with space":  {[]string{"pods", "-m", "web"}, "web", "pods"},
		"short with equals": {[]string{"pods", "-m=web"}, "web", "pods"},
		"absent":            {[]string{"pods", "-n", "prod"}, "", "pods -n prod"},
		"last wins":         {[]string{"pods", "-m", "a", "-m", "b"}, "b", "pods"},
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
