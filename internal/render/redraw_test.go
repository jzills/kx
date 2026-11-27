package render

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestRedrawTableWritesCaptionAndTable(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	headers := []string{"NAME", "STATUS"}
	rows := [][]string{{"nginx", "Running"}}
	lines := r.redrawTable(headers, rows, Frame{}, true, 80, 0, "", "Pods", "prod", "watching").Lines

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

	r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, Frame{}, true, 80, 0, "")
	out.Reset()
	r.redrawTable([]string{"NAME"}, [][]string{{"b"}}, Frame{Lines: 2, Width: 80}, true, 80, 0, "")

	got := out.String()
	if !strings.HasPrefix(got, "\x1b[2A\x1b[J") {
		t.Errorf("output = %q, want it to start with the clear sequence", got)
	}
}

func TestRedrawTableNoopOffTerminal(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, Frame{}, false, 80, 0, "").Lines

	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing written when disabled", out.String())
	}
	if lines != 0 {
		t.Errorf("lines = %d, want 0", lines)
	}
}

// A listing reads best as one line per resource, so the NAME column must
// flex — shrink and ellipsize — rather than let a row wider than the terminal
// wrap, the same mechanism fitFlexColumn already provides for any other
// table. What keeps the cursor-up math right is the count of lines the table
// took, not this: flexing gives up at minFlexWidth and the table overflows
// anyway. See TestRedrawTableCountsTheLinesAWrappedRowTakes.
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

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"nginx"}}, Frame{}, true, 80, 0,
		"press Ctrl-C to stop", "Pods", "prod", "watching").Lines

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

	lines := r.redrawTable([]string{"NAME"}, [][]string{{"nginx"}}, Frame{}, true, 80, 0, "", "Pods", "prod").Lines

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
	if lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, Frame{}, true, 120, 0, footer, "Pods").Lines; lines != 4 {
		t.Errorf("at 120 columns: lines = %d, want 4 (caption, header, row, footer)", lines)
	}
	if lines := r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, Frame{}, true, 80, 0, footer, "Pods").Lines; lines != 5 {
		t.Errorf("at 80 columns: lines = %d, want 5, the footer taking two", lines)
	}
}

// A row wider than the terminal wraps onto a second physical line, and the
// next redraw moves the cursor up by the count returned, so the count
// includes each line the header and the rows wrap onto. Counted as one line
// each, every frame left the un-erased top of the last one on screen — a
// `kx get pods -A -w` in a terminal under about 95 columns accumulated one
// stale copy of the table per watch event (#731).
//
// NAME flexes, so a row only overflows once that has shrunk as far as it
// goes: at minFlexWidth the two columns below take 25 display columns, which
// wraps at a width of 20. The same table at a width it fits is the control.
func TestRedrawTableCountsTheLinesAWrappedRowTakes(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	headers := []string{"NAME", "STATUS"}
	rows := [][]string{{strings.Repeat("n", 40), "Running"}}

	if lines := r.redrawTable(headers, rows, Frame{}, true, 120, 0, "", "Pods").Lines; lines != 3 {
		t.Errorf("at 120 columns: lines = %d, want 3 (caption, header, row)", lines)
	}
	if lines := r.redrawTable(headers, rows, Frame{}, true, 20, 0, "", "Pods").Lines; lines != 5 {
		t.Errorf("at 20 columns: lines = %d, want 5, the header and the row taking two each", lines)
	}
}

// A line of exactly the terminal's width takes one line, not two: a terminal
// defers the wrap until one more cell is written. Counted as two, the
// cursor-up would overshoot the frame and eat the line above it.
func TestRedrawTableCountsAFullWidthRowAsOneLine(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	headers := []string{"NAME", "STATUS"}
	rows := [][]string{{strings.Repeat("n", 40), "Running"}}

	if lines := r.redrawTable(headers, rows, Frame{}, true, 25, 0, "", "Pods").Lines; lines != 3 {
		t.Errorf("at 25 columns, the width the table fills exactly: lines = %d, want 3", lines)
	}
	if lines := r.redrawTable(headers, rows, Frame{}, true, 24, 0, "", "Pods").Lines; lines != 5 {
		t.Errorf("at 24 columns, one short: lines = %d, want 5", lines)
	}
}

// A frame taller than the terminal can't be cleared: the cursor-up stops at
// the top of the screen, so every redraw left the part above it in
// scrollback as another copy of the table (#732). The frame keeps to one
// line under the terminal's height — its last newline would scroll a full
// screen by one — showing the first rows and a line counting the rest. The
// same rows with no height limit are the control: all of them, and no note.
func TestRedrawTableFitsATallTableToTheTerminal(t *testing.T) {
	var rows [][]string
	for i := 1; i <= 20; i++ {
		rows = append(rows, []string{fmt.Sprintf("pod-%02d", i), "Running"})
	}
	headers := []string{"NAME", "STATUS"}
	const footer = "press Ctrl-C to stop"

	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	frame := r.redrawTable(headers, rows, Frame{}, true, 80, 10, footer, "Pods")
	got := out.String()
	// caption, header, five rows, the note, the footer: nine of ten lines.
	if frame != (Frame{Lines: 9, Width: 80, Height: 10}) {
		t.Errorf("frame = %+v, want 9 lines at 80x10", frame)
	}
	if lines := strings.Count(got, "\n"); lines != 9 {
		t.Errorf("wrote %d lines, want 9:\n%s", lines, got)
	}
	if !strings.Contains(got, "pod-05") || strings.Contains(got, "pod-06") {
		t.Errorf("want rows pod-01 to pod-05 and no more:\n%s", got)
	}
	if !strings.Contains(got, "15 more rows don't fit — make the terminal taller to see them\n") {
		t.Errorf("want the note counting 15 rows left out:\n%s", got)
	}
	if note, end := strings.Index(got, "15 more rows"), strings.Index(got, footer); note < 0 || end < note {
		t.Errorf("want the note above the footer:\n%s", got)
	}

	out.Reset()
	frame = r.redrawTable(headers, rows, Frame{}, true, 80, 0, footer, "Pods")
	got = out.String()
	if frame.Lines != 23 || !strings.Contains(got, "pod-20") || strings.Contains(got, "don't fit") {
		t.Errorf("with no height limit: frame = %+v, want all 20 rows and no note:\n%s", frame, got)
	}
}

// Rows that exactly fill the room a frame has are all drawn, with no note:
// the note is only for rows left out, and counting it in anyway would hide
// one row that fit.
func TestRedrawTableDrawsRowsThatExactlyFit(t *testing.T) {
	var rows [][]string
	for i := 1; i <= 6; i++ {
		rows = append(rows, []string{fmt.Sprintf("pod-%02d", i)})
	}
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	// caption, header, six rows, footer: nine lines, one under ten.
	frame := r.redrawTable([]string{"NAME"}, rows, Frame{}, true, 80, 10, "footer", "Pods")
	if got := out.String(); frame.Lines != 9 || !strings.Contains(got, "pod-06") || strings.Contains(got, "don't fit") {
		t.Errorf("frame = %+v, want all six rows and no note:\n%s", frame, got)
	}
}

// A terminal resized since the last frame has rewrapped it, so the count it
// was drawn at no longer says where it starts: moving up that many lines
// after widening overshot into the output above the watch and erased it.
// A resize clears the screen and draws from its top instead. The same
// previous frame at an unchanged size is the control, cleared by its count.
func TestRedrawTableClearsTheScreenAfterAResize(t *testing.T) {
	previous := Frame{Lines: 43, Width: 80, Height: 50}
	for _, tc := range []struct {
		name          string
		width, height int
		want          string
	}{
		{"same size", 80, 50, "\x1b[43A\x1b[J"},
		{"wider", 140, 50, "\x1b[H\x1b[J"},
		{"narrower", 60, 50, "\x1b[H\x1b[J"},
		{"shorter", 80, 30, "\x1b[H\x1b[J"},
	} {
		var out bytes.Buffer
		r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
		r.redrawTable([]string{"NAME"}, [][]string{{"a"}}, previous, true, tc.width, tc.height, "")
		if got := out.String(); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: output = %q, want it to start with %q", tc.name, got, tc.want)
		}
	}
}

// A watch asks on its tick whether the terminal changed size, so it redraws
// for the new size without waiting for an event. A buffer is pipeWidth wide
// with no height, so a frame drawn at that size is the control; a frame that
// drew nothing has nothing to redraw.
func TestResizedComparesTheFrameWithTheTerminal(t *testing.T) {
	var out bytes.Buffer
	r := newWithProfile(&out, &out, "github-dark", termenv.Ascii)
	for _, tc := range []struct {
		frame Frame
		want  bool
	}{
		{Frame{Lines: 3, Width: pipeWidth}, false},
		{Frame{Lines: 3, Width: 80, Height: 24}, true},
		{Frame{Lines: 3, Width: pipeWidth, Height: 24}, true},
		{Frame{}, false},
	} {
		if got := r.resized(tc.frame); got != tc.want {
			t.Errorf("resized(%+v) = %v, want %v", tc.frame, got, tc.want)
		}
	}
}
