package mark

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The favicon is the mark with its shadow left off, centred in a square tile.
//
// Each half of that is load-bearing and neither shows up in a diff: a favicon
// that let the shadow through would go muddy at the 16 pixels a tab draws it
// at, which is the whole reason it is drawn from Blocks, and one whose tile
// clipped the mark would lose a letter in every place the icon appears.
func TestFaviconTileHoldsTheWholeMark(t *testing.T) {
	blocks := 0
	for _, line := range Lines {
		blocks += strings.Count(line, "█")
	}

	shapes, side := FaviconShapes()
	if len(shapes) != blocks {
		t.Errorf("tile holds %d shapes, want the %d blocks in the art",
			len(shapes), blocks)
	}
	for _, shape := range shapes {
		if shape.Width != cellWidth || shape.Height != cellHeight {
			t.Errorf("tile holds a %gx%g shape; a favicon is whole cells only, "+
				"so no shadow stroke should have reached it",
				shape.Width, shape.Height)
		}
		if shape.X < 0 || shape.Y < 0 ||
			shape.X+shape.Width > side || shape.Y+shape.Height > side {
			t.Errorf("block at %g,%g falls outside the %gx%g tile",
				shape.X, shape.Y, side, side)
		}
	}
}

// The colour is the one thing a caller chooses, and the reports choose it per
// palette. An SVG that dropped it would render black on a dark tab.
func TestFaviconCarriesTheFill(t *testing.T) {
	svg := Favicon("#bd93f9")
	if !strings.Contains(svg, `fill="#bd93f9"`) {
		t.Errorf("favicon does not carry the fill it was given: %s", svg)
	}
	if shapes, _ := FaviconShapes(); strings.Count(svg, "<rect") != len(shapes) {
		t.Errorf("favicon draws %d rects, want %d",
			strings.Count(svg, "<rect"), len(shapes))
	}
}

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
