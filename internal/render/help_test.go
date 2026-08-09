package render

import (
	"bytes"
	"strings"
	"testing"
)

// A command with children (state, after gaining back/drop/forward) lists
// them under their own "Commands" header, the same way the root help screen
// lists sections — just one level down, and unsectioned since three items
// don't need grouping.
func TestCommandHelpRendersCommandsSection(t *testing.T) {
	var out bytes.Buffer
	SetOutput(&out, &out, "github-dark")

	ShowCommandHelp(CommandHelp{
		Path:  "kx state",
		Doc:   "Show current state.",
		Usage: "kx state [OPTIONS] [position]",
		Commands: []HelpItem{
			{Name: "back", Doc: "Navigate to the previous kx get result."},
			{Name: "drop", Doc: "Remove a history entry by position."},
			{Name: "forward", Doc: "Navigate to the next kx get result."},
		},
	})

	got := out.String()
	if !strings.Contains(got, "Commands") {
		t.Errorf("output = %q, want it to contain a Commands header", got)
	}
	for _, name := range []string{"back", "drop", "forward"} {
		if !strings.Contains(got, name) {
			t.Errorf("output missing command %q", name)
		}
	}
}

// A leaf command (no children) renders no Commands section at all, rather
// than an empty header.
func TestCommandHelpOmitsCommandsSectionWhenLeaf(t *testing.T) {
	var out bytes.Buffer
	SetOutput(&out, &out, "github-dark")

	ShowCommandHelp(CommandHelp{
		Path:  "kx scale",
		Doc:   "Scale a workload.",
		Usage: "kx scale [OPTIONS] <index> <replicas>",
	})

	if strings.Contains(out.String(), "Commands") {
		t.Errorf("output = %q, want no Commands header for a leaf command", out.String())
	}
}
