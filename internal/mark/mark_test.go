package mark

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The favicon is the icon centered in a square tile, every rectangle of it
// inside the tile and the tile no wider than the icon plus its margin.
//
// Both halves are load-bearing and neither shows in a diff: a tile that
// clipped would cut a bracket off in every place the icon appears, and one
// sized past the icon would shrink it in the sixteen pixels a tab gives it,
// which is the size the icon exists for.
func TestFaviconTileHoldsTheWholeIcon(t *testing.T) {
	drawn, width, height := iconShapes()

	shapes, side := FaviconShapes()
	if len(shapes) != len(drawn) {
		t.Errorf("tile holds %d shapes, want the icon's %d", len(shapes), len(drawn))
	}
	for _, shape := range shapes {
		if shape.X < 0 || shape.Y < 0 ||
			shape.X+shape.Width > side || shape.Y+shape.Height > side {
			t.Errorf("a %gx%g rectangle at %g,%g falls outside the %gx%g tile",
				shape.Width, shape.Height, shape.X, shape.Y, side, side)
		}
	}

	longest := width
	if height > longest {
		longest = height
	}
	if want := longest + 2*iconPadding; side != want {
		t.Errorf("tile is %g across for a %gx%g icon, want %g", side, width, height, want)
	}

	// The tile the lettermark's icons were drawn in, which opticalDrop is one
	// pixel of at 16. The icon is a different shape in the same tile, and the
	// drop stays true only while the number does.
	if side != 288 {
		t.Errorf("tile is %g units, want 288 — opticalDrop is no longer one "+
			"pixel of a 16-pixel icon", side)
	}
}

// What makes the icon the kx mark rather than nine rectangles: the eye sits
// centered on both axes, clear of both brackets, and the feet stand under
// them. Each of those is a thing a careless edit to iconCells breaks while
// leaving a plausible-looking shape behind, and no test of the tile would
// notice.
func TestIconHoldsItsEyeBetweenTheBrackets(t *testing.T) {
	shapes, width, height := iconShapes()
	if len(shapes) != len(iconCells) {
		t.Fatalf("iconShapes drew %d rectangles for %d cells",
			len(shapes), len(iconCells))
	}

	// The widest rectangle on the icon's horizontal midline is the eye; the
	// uprights are the full-height ones at either end.
	eye := shapes[6]
	left, right := shapes[0], shapes[3]

	if got, want := eye.X+eye.Width/2, width/2; got != want {
		t.Errorf("eye's center is at x=%g, want the icon's own center %g", got, want)
	}
	if got, want := eye.Y+eye.Height/2, (left.Y+left.Height)/2; got != want {
		t.Errorf("eye's center is at y=%g, want the brackets' center %g", got, want)
	}
	if gap := eye.X - (left.X + left.Width); gap <= 0 {
		t.Errorf("eye overlaps the left bracket by %g; the brackets hold it, "+
			"they do not touch it", -gap)
	}
	if gap := right.X - (eye.X + eye.Width); gap <= 0 {
		t.Errorf("eye overlaps the right bracket by %g", -gap)
	}

	for i, foot := range []Rect{shapes[7], shapes[8]} {
		if foot.Y != left.Y+left.Height {
			t.Errorf("foot %d starts at y=%g, want the brackets' bottom %g",
				i, foot.Y, left.Y+left.Height)
		}
		if bottom := foot.Y + foot.Height; bottom != height {
			t.Errorf("foot %d ends at y=%g, want the icon's bottom %g", i, bottom, height)
		}
	}
}

// A tab icon sits below the center of its tile on purpose: the browser centers
// it against a line box that reserves room for descenders, so a mark centered
// in its own tile reads as floating above the title beside it.
//
// The drop is one pixel at the sixteen a tab draws an icon at — the smallest
// move available there — so this pins both that it happens and that it stays
// small, in the units the tile is measured in rather than in pixels.
func TestTabIconSitsBelowTheTileCenter(t *testing.T) {
	centered, side := FaviconShapes()
	dropped, tabSide := TabIconShapes()

	if tabSide != side {
		t.Errorf("tab icon tile is %g, want the same %g the centered tile uses",
			tabSide, side)
	}
	if len(dropped) != len(centered) {
		t.Fatalf("tab icon has %d shapes, want %d", len(dropped), len(centered))
	}

	for i := range dropped {
		if dropped[i].X != centered[i].X {
			t.Errorf("shape %d moved sideways, from %g to %g; the drop is vertical",
				i, centered[i].X, dropped[i].X)
		}
		if drop := dropped[i].Y - centered[i].Y; drop != opticalDrop {
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

// The color is the one thing a caller chooses, and the reports choose it per
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
