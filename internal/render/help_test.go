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

// A name longer than the column budget must not drag the description column
// off the right edge with it.
//
// Reachable in the wild since #322: the Files block names whatever KX_CONFIG
// and KX_STATE point at, so those two names are the caller's text rather than
// kx's. A 106-character state path made the name column 110 wide on a screen
// capped at 92, so every description wrapped to wrapText's 20-character floor
// and hung 78 columns off the end — measured at 187 columns.
//
// The path itself is exempt: wrapText already leaves a long word whole because
// a split one can't be clicked or copied, and a path is that case exactly.
func TestItemBlockKeepsDescriptionsInsideTheWidthCap(t *testing.T) {
	var out bytes.Buffer
	SetOutput(&out, &out, "github-dark")

	path := "/tmp/scratch/a/very/deeply/nested/directory/for/one/shell/state.json"
	doc := "Saved listings, navigated with kx state"
	ShowRootHelp(RootHelp{Files: []HelpItem{{Name: path, Doc: doc}}})

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, path) {
			continue
		}
		if len(line) > proseMaxWidth {
			t.Errorf("line is %d columns, want at most %d:\n%q", len(line), proseMaxWidth, line)
		}
	}
	if !strings.Contains(out.String(), doc) {
		t.Errorf("output = %q,\nwant the description on one unbroken line", out.String())
	}
}

// The ordinary case is untouched: names that fit still line their descriptions
// up in one column, which is what makes the screen scannable.
func TestItemBlockStillAlignsNamesThatFit(t *testing.T) {
	var out bytes.Buffer
	SetOutput(&out, &out, "github-dark")

	ShowRootHelp(RootHelp{Files: []HelpItem{
		{Name: "~/.kx/config.toml", Doc: "Settings"},
		{Name: "~/.kx/state.json", Doc: "Saved listings"},
	}})

	var columns []int
	for _, line := range strings.Split(out.String(), "\n") {
		for _, doc := range []string{"Settings", "Saved listings"} {
			if i := strings.Index(line, doc); i >= 0 {
				columns = append(columns, i)
			}
		}
	}
	if len(columns) != 2 {
		t.Fatalf("found %d description lines, want 2:\n%s", len(columns), out.String())
	}
	if columns[0] != columns[1] {
		t.Errorf("descriptions start at columns %d and %d, want one column", columns[0], columns[1])
	}
}
