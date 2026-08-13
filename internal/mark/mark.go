// Package mark holds the kx logo, as the one set of lines every drawing of it
// is made from.
//
// kx draws its mark in two ways that cannot share code: render prints these
// lines to a terminal, and tools/gen-marks redraws them as SVG rectangles for
// the README banner, the site and the --html report masthead. They used to
// share the art by each keeping its own copy of the literal, which is exactly
// the arrangement where one gets edited and the other doesn't.
package mark

// Lines is the mark, drawn in full blocks with a single-line box-drawing
// shadow.
//
// The shadow is single-line rather than double because gen-marks draws each
// box-drawing character as one stroke: a double-lined source was the one place
// the terminal and the vectors disagreed, printing a doubled hairline outline
// against the single clean one every SVG of the mark shows.
//
// gen-marks maps each character here onto the shapes it contributes, and exits
// non-zero on one it has no shape for — so a glyph added here that the vectors
// can't draw fails the generator rather than quietly dropping out of them.
var Lines = []string{
	"██┐  ██┐██┐  ██┐",
	"██│ ██┌┘└██┐██┌┘",
	"█████┌┘  └███┌┘ ",
	"██┌─██┐  ██┌██┐ ",
	"██│  ██┐██┌┘ ██┐",
	"└─┘  └─┘└─┘  └─┘",
}
