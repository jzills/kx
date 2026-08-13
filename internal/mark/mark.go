// Package mark holds the kx logo, as the one set of lines every drawing of it
// is made from, and the geometry that turns those lines into shapes.
//
// kx draws its mark in three ways: render prints the lines to a terminal,
// tools/gen-marks redraws them as the SVG files the README banner, the site
// and the --html report masthead embed, and web builds a favicon from them at
// render time, in the palette the report was rendered with. The lines used to
// be a literal copied into each drawing, which is exactly the arrangement
// where one gets edited and the others don't.
package mark

import (
	"fmt"
	"strings"
)

// Lines is the mark, drawn in full blocks with a single-line box-drawing
// shadow.
//
// The shadow is single-line rather than double because gen-marks draws each
// box-drawing character as one stroke: a double-lined source was the one place
// the terminal and the vectors disagreed, printing a doubled hairline outline
// against the single clean one every SVG of the mark shows.
//
// Shapes maps each character here onto the rectangles it contributes, and
// panics on one it has no shape for — so a glyph added here that the vectors
// can't draw fails the generator rather than quietly dropping out of them.
var Lines = []string{
	"██┐  ██┐██┐  ██┐",
	"██│ ██┌┘└██┐██┌┘",
	"█████┌┘  └███┌┘ ",
	"██┌─██┐  ██┌██┐ ",
	"██│  ██┐██┌┘ ██┐",
	"└─┘  └─┘└─┘  └─┘",
}

// Cell geometry, in the proportions the mark was designed at: a monospace cell
// at font-size 30 with 36 of line height is 18 wide and 36 tall.
const (
	cellWidth  = 18.0
	cellHeight = 36.0
	// stroke is how thick a box-drawing line is drawn. Thicker than the
	// hairline a font would set it at: at the size the mark is displayed a
	// true one-pixel line disappears, and this is the weight the shadow reads
	// at beside 18-wide blocks.
	stroke = 5.0
)

// Rect is one rectangle of the drawn mark, in the grid's own units.
type Rect struct{ X, Y, Width, Height float64 }

// Shapes returns every rectangle the mark is drawn from, in reading order,
// with the size of the grid they fill.
//
// Blocks are whole cells; the box-drawing characters are drawn as the lines
// they represent, a corner being a half-width arm meeting a half-height one,
// so adjacent cells join seamlessly into the continuous outline the art draws
// around the letterforms.
func Shapes() (shapes []Rect, width, height float64) {
	columns := 0
	for row, line := range Lines {
		column := 0
		for _, glyph := range line {
			shapes = append(shapes,
				cell(glyph, float64(column)*cellWidth, float64(row)*cellHeight)...)
			column++
		}
		if column > columns {
			columns = column
		}
	}
	return shapes, float64(columns) * cellWidth, float64(len(Lines)) * cellHeight
}

// Blocks returns only the filled cells — the mark without its shadow.
//
// What a favicon is drawn from. The shadow is a single stroke of a cell's
// width, so at the 16 and 32 pixels a browser tab renders it at, it is thinner
// than a pixel: it stops reading as an outline and just muddies the
// letterforms it is drawn around. The blocks alone stay legible all the way
// down.
func Blocks() []Rect {
	var shapes []Rect
	for row, line := range Lines {
		for column, glyph := range []rune(line) {
			if glyph != '█' {
				continue
			}
			shapes = append(shapes,
				cell(glyph, float64(column)*cellWidth, float64(row)*cellHeight)...)
		}
	}
	return shapes
}

// cell returns the shapes one character contributes, positioned at x,y.
func cell(glyph rune, x, y float64) []Rect {
	var (
		midX = x + (cellWidth-stroke)/2
		midY = y + (cellHeight-stroke)/2
		// Arms run from the cell edge to the centre of the stroke, so two
		// neighbouring cells overlap by nothing and leave no gap.
		leftArm  = Rect{x, midY, (cellWidth-stroke)/2 + stroke, stroke}
		rightArm = Rect{midX, midY, (cellWidth-stroke)/2 + stroke, stroke}
		upArm    = Rect{midX, y, stroke, (cellHeight-stroke)/2 + stroke}
		downArm  = Rect{midX, midY, stroke, (cellHeight-stroke)/2 + stroke}
	)

	switch glyph {
	case '█':
		return []Rect{{x, y, cellWidth, cellHeight}}
	case '│':
		return []Rect{{midX, y, stroke, cellHeight}}
	case '─':
		return []Rect{{x, midY, cellWidth, stroke}}
	case '┐':
		return []Rect{leftArm, downArm}
	case '┌':
		return []Rect{rightArm, downArm}
	case '┘':
		return []Rect{leftArm, upArm}
	case '└':
		return []Rect{rightArm, upArm}
	case ' ':
		return nil
	default:
		panic(fmt.Sprintf("mark: no shape for %q", glyph))
	}
}

// faviconPadding is the margin left around the mark in a favicon tile, in grid
// units — half a cell, enough that the letterforms don't sit flush against the
// edge of the tile at large sizes without spending pixels that matter at 16.
const faviconPadding = cellWidth / 2

// FaviconShapes returns the blocks centred in a square tile, with the tile's
// side.
//
// Square because every consumer of a favicon is: the mark's own 15:5 block
// grid is centred in the tile rather than stretched to it, and the tile is
// sized from the blocks actually drawn rather than the full line grid, whose
// last row and column are shadow the favicon leaves out.
//
// Separate from Favicon so the SVG and the PNGs tools/gen-marks rasterises are
// the same shapes in the same tile — an icon set whose sizes disagreed about
// where the mark sits would be visible as a jump when a browser switched
// between them.
func FaviconShapes() (shapes []Rect, side float64) {
	blocks := Blocks()
	var maxX, maxY float64
	for _, block := range blocks {
		if right := block.X + block.Width; right > maxX {
			maxX = right
		}
		if bottom := block.Y + block.Height; bottom > maxY {
			maxY = bottom
		}
	}

	side = maxX
	if maxY > side {
		side = maxY
	}
	side += 2 * faviconPadding

	shapes = make([]Rect, 0, len(blocks))
	for _, block := range blocks {
		shapes = append(shapes, Rect{
			X:      block.X + (side-maxX)/2,
			Y:      block.Y + (side-maxY)/2,
			Width:  block.Width,
			Height: block.Height,
		})
	}
	return shapes, side
}

// Favicon returns the mark as a square SVG document filled with fill, which is
// a CSS color the caller is responsible for: the reports pass their palette's
// accent, so the tab icon carries the theme the page was rendered with.
func Favicon(fill string) string {
	shapes, side := FaviconShapes()

	var out strings.Builder
	fmt.Fprintf(&out,
		"<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %g %g\" "+
			"fill=\"%s\" shape-rendering=\"crispEdges\" role=\"img\" aria-label=\"kx\">",
		side, side, fill)
	for _, shape := range shapes {
		fmt.Fprintf(&out, "<rect x=\"%g\" y=\"%g\" width=\"%g\" height=\"%g\"/>",
			shape.X, shape.Y, shape.Width, shape.Height)
	}
	out.WriteString("</svg>")
	return out.String()
}
