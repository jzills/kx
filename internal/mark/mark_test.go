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

// A tab icon sits below the centre of its tile on purpose: the browser centres
// it against a line box that reserves room for descenders, so a mark centred
// in its own tile reads as floating above the title beside it.
//
// The drop is one pixel at the sixteen a tab draws an icon at — the smallest
// move available there — so this pins both that it happens and that it stays
// small, in the units the tile is measured in rather than in pixels.
func TestTabIconSitsBelowTheTileCentre(t *testing.T) {
	centred, side := FaviconShapes()
	dropped, tabSide := TabIconShapes()

	if tabSide != side {
		t.Errorf("tab icon tile is %g, want the same %g the centred tile uses",
			tabSide, side)
	}
	if len(dropped) != len(centred) {
		t.Fatalf("tab icon has %d shapes, want %d", len(dropped), len(centred))
	}

	for i := range dropped {
		if dropped[i].X != centred[i].X {
			t.Errorf("shape %d moved sideways, from %g to %g; the drop is vertical",
				i, centred[i].X, dropped[i].X)
		}
		if drop := dropped[i].Y - centred[i].Y; drop != opticalDrop {
			t.Errorf("shape %d dropped by %g, want %g", i, drop, opticalDrop)
		}
	}

	// One pixel at 16, and no more: a mark that fell far enough to look low is
	// the same defect the other way up.
	if pixels := opticalDrop / side * 16; pixels != 1 {
		t.Errorf("drop is %g pixels of a 16-pixel icon, want exactly 1", pixels)
	}

	var lowest float64
	for _, shape := range dropped {
		if bottom := shape.Y + shape.Height; bottom > lowest {
			lowest = bottom
		}
	}
	if lowest > side {
		t.Errorf("the drop pushes the mark to %g, past the %g tile", lowest, side)
	}
}

// The colour is the one thing a caller chooses, and the reports choose it per
// palette. An SVG that dropped it would render black on a dark tab.
func TestFaviconCarriesTheFill(t *testing.T) {
	svg := Favicon("#bd93f9")
	if !strings.Contains(svg, `fill="#bd93f9"`) {
		t.Errorf("favicon does not carry the fill it was given: %s", svg)
	}
	if shapes, _ := TabIconShapes(); strings.Count(svg, "<rect") != len(shapes) {
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
