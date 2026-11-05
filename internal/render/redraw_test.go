package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestRedrawTableWritesCaptionAndTable(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	headers := []string{"NAME", "STATUS"}
	rows := [][]string{{"nginx", "Running"}}
	lines := r.redrawTable(headers, rows, 0, true, 80, "", "Pods", "prod", "watching")

	got := out.String()
	if !strings.Contains(got, "Pods · prod · watching") {
		t.Errorf("output = %q, want the caption", got)
	}
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "nginx") {
		t.Errorf("output = %q, want header and row", got)
	}
	// caption line + header line + 1 body row
	if lines != 3 {
		t.Errorf("lines = %d, want 3", lines)
	}
}

func TestRedrawTableClearsPreviousFrame(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, 0, true, 80, "")
	out.Reset()
	r.redrawTable([]string{"NAME"}, [][]string{{"b"}}, 2, true, 80, "")

	got := out.String()
	if !strings.HasPrefix(got, "\x1b[2A\x1b[J") {
		t.Errorf("output = %q, want it to start with the clear sequence", got)
	}
}

func TestRedrawTableNoopOffTerminal(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, 0, false, 80, "")

	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing written when disabled", out.String())
	}
	if lines != 0 {
		t.Errorf("lines = %d, want 0", lines)
	}
}

// RedrawTable's \x1b[NA cursor-up math assumes exactly one physical
// terminal line per printed row. A row wider than the terminal wraps onto a
// second physical line, and the next frame's cursor-up then lands short,
// leaving stale fragments of the previous frame interleaved with the new
// one — the NAME column must flex (shrink and ellipsize) instead of letting
// that happen, the same mechanism fitFlexColumn already provides for any
// other table.
func TestRedrawTableFlexesNameColumn(t *testing.T) {
	columns, _ := styledColumnsAndCells([]string{"NAME", "STATUS"}, [][]string{{"a", "Running"}})
	enableNameFlex([]string{"NAME", "STATUS"}, columns)
	if !columns[0].Flex {
		t.Error("NAME column should be Flex")
	}
	if columns[1].Flex {
		t.Error("STATUS column should not be Flex")
	}
}

func TestRedrawTableFlexNoNameColumnIsNoop(t *testing.T) {
	columns, _ := styledColumnsAndCells([]string{"FOO"}, [][]string{{"a"}})
	enableNameFlex([]string{"FOO"}, columns) // must not panic
	if columns[0].Flex {
		t.Error("no NAME column present, nothing should be flexed")
	}
}

// A standing note about the live view belongs under the table it describes,
// and has to be redrawn with it: the redraw clears its whole frame, so a note
// printed under the table once is erased on the next event, and one printed
// above the loop scrolls away from the thing it explains.
func TestRedrawTableDrawsTheFooterUnderTheTable(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"nginx"}}, 0, true, 80,
		"press Ctrl-C to stop", "Pods", "prod", "watching")

	got := out.String()
	note := strings.Index(got, "Ctrl-C")
	row := strings.Index(got, "nginx")
	if note < 0 {
		t.Fatalf("output = %q, want the footer drawn", got)
	}
	if row < 0 || note < row {
		t.Errorf("output = %q, want the footer below the table", got)
	}
	// caption + header + one row + footer. A footer left out of the count is
	// scrolled off by the next frame's cursor-up rather than overwritten.
	if lines != 4 {
		t.Errorf("lines = %d, want 4 — the footer has to be in the count", lines)
	}
}

// No footer, no line: the count has to stay exact or every later frame is
// misaligned by one.
func TestRedrawTableWithoutAFooterCountsOnlyTheTable(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"nginx"}}, 0, true, 80, "", "Pods", "prod")

	if lines != 3 {
		t.Errorf("lines = %d, want 3 (caption + header + row)", lines)
	}
}

// A footer wider than the terminal wraps, and the next redraw moves the
// cursor up by the count returned, so the count includes each line it
// wraps onto: counted as one, every frame left a line of the last on
// screen. The same footer at a width it fits is the control.
func TestRedrawTableCountsTheLinesAWrappedFooterTakes(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	footer := strings.Repeat("x", 100)
	if lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, 0, true, 120, footer, "Pods"); lines != 4 {
		t.Errorf("at 120 columns: lines = %d, want 4 (caption, header, row, footer)", lines)
	}
	if lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, 0, true, 80, footer, "Pods"); lines != 5 {
		t.Errorf("at 80 columns: lines = %d, want 5, the footer taking two", lines)
	}
}
