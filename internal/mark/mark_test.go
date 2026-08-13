package mark

import (
	"testing"
	"unicode/utf8"
)

// The mark is a grid: render prints one line under the next, and gen-marks
// walks each line into a column of cells. A line short of the others is
// invisible in both — the terminal just prints a stubby row, and the generator
// draws fewer rects on it and sizes the canvas from the widest line — so the
// only place a ragged edit gets caught is here.
func TestLinesAreOneWidth(t *testing.T) {
	width := utf8.RuneCountInString(Lines[0])
	for i, line := range Lines {
		if got := utf8.RuneCountInString(line); got != width {
			t.Errorf("line %d is %d cells wide, want %d (line 0's width): %q",
				i, got, width, line)
		}
	}
}
