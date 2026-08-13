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
// to stderr — so the caption reports a count rather than printing nothing.
func TestIndexedTableCaptionsAnEmptyListing(t *testing.T) {
	out := capture(func(r *Renderer) { r.IndexedTable(index.Table{}, "pods", "prod") })

	if !strings.Contains(out, "0 items") {
		t.Errorf("output = %q, want a zero-count caption", out)
	}
}
