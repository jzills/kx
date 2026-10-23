package main

import (
	"errors"
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
