package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/theme"
)

// An unknown theme or engine sends the user to kx theme or kx engine, so that
// command has to run, on the default, while every other command still refuses.
// Before, kx theme refused too, and editing the file by hand was the only way
// out.
func TestOnlyTheFixingCommandRunsPastAnUnknownName(t *testing.T) {
	unknownTheme := config.UnknownNameError{Setting: "theme", Name: "bogus"}
	unknownEngine := config.UnknownNameError{Setting: "engine", Name: "nope"}
	for _, tc := range []struct {
		err  error
		args []string
		want bool
	}{
		{unknownTheme, []string{"theme"}, true},
		{unknownTheme, []string{"--no-color", "theme", "dracula"}, true},
		{unknownTheme, []string{"engine"}, false},
		{unknownTheme, []string{"get", "pods"}, false},
		{unknownTheme, nil, false},
		{unknownEngine, []string{"engine", "trivy"}, true},
		{unknownEngine, []string{"scan"}, false},
		{errors.New("Invalid value for 'KX_MAX_HISTORY'."), []string{"theme"}, false},
	} {
		cfg := config.Config{Theme: "bogus", Engine: "nope"}
		if got := fixesUnknownName(tc.err, &cfg, tc.args); got != tc.want {
			t.Errorf("fixesUnknownName(%v, %q) = %v, want %v", tc.err, tc.args, got, tc.want)
		}
		if !tc.want {
			continue
		}
		if tc.err == unknownTheme && cfg.Theme != theme.Default {
			t.Errorf("%q: Theme = %q, want the default %q", tc.args, cfg.Theme, theme.Default)
		}
		if tc.err == unknownEngine && cfg.Engine != config.DefaultEngine {
			t.Errorf("%q: Engine = %q, want the default %q", tc.args, cfg.Engine, config.DefaultEngine)
		}
	}
}

// Run as kubectl_complete-idx, kx completes the arguments kubectl hands it,
// which is how kubectl idx <TAB> — and the krew alias in zsh, which expands to
// it — reaches kx's completion. The control is the same arguments under kx's
// own name, which run them.
func TestCompletionHelperNameCompletesItsArguments(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want []string
	}{
		{[]string{"/home/u/.krew/bin/kubectl_complete-idx", "get", "pods", ""}, []string{"__complete", "get", "pods", ""}},
		{[]string{"C:/krew/bin/kubectl_complete-idx.exe", "describe", ""}, []string{"__complete", "describe", ""}},
		{[]string{"/usr/local/bin/kx", "get", "pods"}, []string{"get", "pods"}},
		{[]string{"/home/u/.krew/bin/kubectl-idx", "get", "pods"}, []string{"get", "pods"}},
	} {
		if got := argsOf(tc.argv); !slices.Equal(got, tc.want) {
			t.Errorf("argsOf(%q) = %q, want %q", tc.argv, got, tc.want)
		}
	}
}
