package render

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/index"
)

// A blank cell has to reach the screen in its own column.
//
// This is the property the whole parse-once change exists for, and no render
// test covered it: IndexedTable used to re-parse the padded text the index
// service handed it, where an empty cell and column padding are the same run of
// spaces. `kubectl config get-contexts` blanks CURRENT on every row but the
// active one, so every value after it slid one column left and each context's
// CLUSTER appeared beneath its NAME.
//
// Asserted on column offsets rather than on the values being present anywhere:
// a shifted row still contains all of them, which is exactly why the bug was
// invisible to a Contains check.
func TestIndexedTableKeepsColumnsAlignedAroundABlankCell(t *testing.T) {
	table := index.Service{}.Add(
		"CURRENT   NAME             CLUSTER\n" +
			"          alt              local\n" +
			"*         docker-desktop   local")

	out := capture(func(r *Renderer) { r.IndexedTable(table, "contexts", "") })

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("output has %d lines, want a caption, a header and two rows:\n%s", len(lines), out)
	}
	header, blankRow, markedRow := lines[1], lines[2], lines[3]

	nameAt := strings.Index(header, "NAME")
	if nameAt < 0 {
		t.Fatalf("header has no NAME column: %q", header)
	}
	if got := strings.Index(blankRow, "alt"); got != nameAt {
		t.Errorf("row with a blank CURRENT put its name at %d, want the NAME column at %d:\n%s",
			got, nameAt, out)
	}
	if got := strings.Index(markedRow, "docker-desktop"); got != nameAt {
		t.Errorf("row with a marked CURRENT put its name at %d, want %d:\n%s", got, nameAt, out)
	}

	currentAt := strings.Index(header, "CURRENT")
	if got := strings.Index(markedRow, "*"); got != currentAt {
		t.Errorf("marker at %d, want the CURRENT column at %d:\n%s", got, currentAt, out)
	}
}

// Output kx cannot number prints exactly as it arrived — JSON and YAML reach
// the terminal through here untouched.
func TestIndexedTablePrintsNonTabularOutputVerbatim(t *testing.T) {
	raw := `{"kind":"PodList","items":[]}`
	table := index.Service{}.Add(raw)

	out := capture(func(r *Renderer) { r.IndexedTable(table, "pods", "prod") })

	if !strings.Contains(out, raw) {
		t.Errorf("output = %q, want the raw document unchanged", out)
	}
}

// Genuinely empty stdout is not an error — kubectl sends "No resources found"
// to stderr — so the caption says nothing was found rather than printing
// nothing itself.
func TestIndexedTableCaptionsAnEmptyListing(t *testing.T) {
	out := capture(func(r *Renderer) { r.IndexedTable(index.Table{}, "pods", "prod") })

	if !strings.Contains(out, "none found") {
		t.Errorf("output = %q, want an empty-listing caption", out)
	}
}

// A table whose Rows the parser returned empty (as opposed to non-tabular,
// genuinely empty stdout above) hits the other empty branch — both must
// caption the same way rather than one falling back to a bare zero count.
func TestIndexedTableCaptionsAnEmptyParsedTable(t *testing.T) {
	table := index.Table{Headers: []string{"NAME", "STATUS"}}
	out := capture(func(r *Renderer) { r.IndexedTable(table, "pods", "prod") })

	if !strings.Contains(out, "none found") {
		t.Errorf("output = %q, want an empty-listing caption", out)
	}
}

// kx marks the active row with → in its other switch-style listings (kx theme,
// kx engine, kx state --all). kx ns marked nothing, so "you are here" lived
// only in the caption — which on that screen is what the caption is for, but
// the row itself said nothing.
func TestSwitchListingMarksTheCurrentRow(t *testing.T) {
	table := index.Table{
		Headers: []string{"X", "NAME", "STATUS", "AGE"},
		Rows: [][]string{
			{"1", "default", "Active", "155d"},
			{"2", "diagnostics", "Active", "65d"},
		},
	}

	out := capture(func(r *Renderer) { r.SwitchListing(table, "namespaces", "diagnostics") })

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("output = %q, want a caption, a header and two rows", out)
	}
	if strings.Contains(lines[2], "→") {
		t.Errorf("row = %q, want no marker on a namespace that is not current", lines[2])
	}
	if !strings.Contains(lines[3], "→") {
		t.Errorf("row = %q, want the current namespace marked", lines[3])
	}
	// The marker sits between the index and the name, the way every other
	// marked listing in kx places it.
	if position := strings.Index(lines[3], "→"); position > strings.Index(lines[3], "diagnostics") {
		t.Errorf("row = %q, want the marker before the name", lines[3])
	}
}

// Status colouring and the caption are the ordinary listing's, unchanged: the
// marker column is the only difference, so a switch listing and a kx get
// listing of the same resource do not drift apart.
func TestSwitchListingKeepsTheOrdinaryCaption(t *testing.T) {
	table := index.Table{
		Headers: []string{"X", "NAME", "STATUS", "AGE"},
		Rows:    [][]string{{"1", "default", "Active", "155d"}},
	}

	out := capture(func(r *Renderer) { r.SwitchListing(table, "namespaces", "default") })

	for _, want := range []string{"Namespaces", "default", "1 item"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q\n  missing %q", out, want)
		}
	}
}

// Nothing to mark is not an error: a current namespace outside the listing —
// or none at all — leaves every row unmarked rather than guessing at one.
func TestSwitchListingMarksNothingWhenTheCurrentRowIsAbsent(t *testing.T) {
	table := index.Table{
		Headers: []string{"X", "NAME", "STATUS", "AGE"},
		Rows:    [][]string{{"1", "default", "Active", "155d"}},
	}

	for _, current := range []string{"", "somewhere-else"} {
		out := capture(func(r *Renderer) { r.SwitchListing(table, "namespaces", current) })
		if strings.Contains(out, "→") {
			t.Errorf("output = %q for current %q, want no marker", out, current)
		}
	}
}
