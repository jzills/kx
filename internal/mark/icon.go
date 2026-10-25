package mark

// The icon mark: two brackets closing on a single block, with two small feet.
//
// It is kx's own notation made into a creature — an index spent on one row,
// which is what every kx command does — and it exists because the lettermark
// cannot do an icon's job. At the sixteen pixels a browser tab draws an icon
// at, "kx" in whole 18-unit blocks is four smudges a reader cannot tell from
// any other two-letter tool's tab. The brackets hold their shape there.
//
// This is a second source of truth beside Lines, which the package's doc
// comment otherwise argues against, so the reason is worth stating: Lines is
// art kx prints to a terminal, and a creature cannot be printed as one. The
// split is deliberate and bounded — the icon is drawn here and nowhere else,
// it is the only thing the icon slots use, and every drawing of the mark that
// has room to be read (the --help banner, the README, the site, the report
// masthead) still comes from Lines.

const (
	// iconUnit is the grid step the icon is drawn on: a bracket's thickness,
	// an arm's height, a foot. Not cellWidth, because the icon is not made of
	// the art's cells — it is its own shape, and at icon sizes it wants a
	// heavier stroke than an 18-wide block grid gives.
	iconUnit = 24.0

	// iconPadding is the margin around the icon in its tile, chosen so the
	// tile measures 288 units — the side the lettermark's tile measured.
	// Keeping that number is what keeps opticalDrop worth exactly one pixel
	// at the sixteen a tab renders an icon at.
	iconPadding = 12.0
)

// iconCells is the icon in grid steps, as x, y, width and height in iconUnits.
// Written in steps rather than units so the shape can be read here: the
// brackets stand nine steps tall at either end of an eleven-step span, the
// block sits three steps square in the middle of both axes, and the feet take
// the tenth row under the brackets' inside edges.
var iconCells = []Rect{
	{0, 0, 1, 9},  // left bracket, upright
	{0, 0, 2, 1},  // its top arm
	{0, 8, 2, 1},  // its bottom arm
	{10, 0, 1, 9}, // right bracket, upright
	{9, 0, 2, 1},  // its top arm
	{9, 8, 2, 1},  // its bottom arm
	{4, 3, 3, 3},  // the eye: one row, held
	{1, 9, 2, 1},  // left foot
	{8, 9, 2, 1},  // right foot
}

// iconShapes returns the icon's rectangles in grid units, with the box they
// cover. The feet are square: the rounded ones the mark was drawn with cannot
// survive the PNG rasterizer, which fills whole rectangles, and an icon whose
// SVG and PNGs disagreed about a corner would show as a jump when a browser
// switched between them.
func iconShapes() (shapes []Rect, width, height float64) {
	shapes = make([]Rect, 0, len(iconCells))
	for _, cell := range iconCells {
		shapes = append(shapes, Rect{
			X:      cell.X * iconUnit,
			Y:      cell.Y * iconUnit,
			Width:  cell.Width * iconUnit,
			Height: cell.Height * iconUnit,
		})
		if right := shapes[len(shapes)-1].X + shapes[len(shapes)-1].Width; right > width {
			width = right
		}
		if bottom := shapes[len(shapes)-1].Y + shapes[len(shapes)-1].Height; bottom > height {
			height = bottom
		}
	}
	return shapes, width, height
}
