package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Table layout matches Rich's `Table(box=None, padding=(0, 2))`, which is what
// the Python implementation renders every listing with: no borders, each cell
// padded two columns on each side, so columns are separated by four spaces and
// every line carries a two-space lead and trail.
//
// The layout is reimplemented rather than delegated to lipgloss/table because
// the output is compared byte-for-byte against the Python renderer; a table
// library with its own padding and border model would drift.
const cellPad = "  "

// Column is a table column. Right-aligns numeric-ish columns the way the Python
// renderer does.
type Column struct {
	Header string
	Right  bool
	// Flex marks the column that absorbs width pressure: when the table is
	// wider than the terminal, this column shrinks and its cells ellipsize
	// rather than letting the shell wrap every row. At most one column should
	// set it; without one the table keeps its natural width.
	Flex bool
}

// minFlexWidth is the narrowest a flexed column shrinks to before the table is
// allowed to overflow — below this the column carries no information.
const minFlexWidth = 10

// ellipsize shortens text to n display columns, marking the cut.
func ellipsize(text string, n int) string {
	if n <= 0 || width(text) <= n {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && width(string(runes))+1 > n {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// Cell is one rendered value. Style names a semantic style; empty leaves the
// text unstyled.
type Cell struct {
	Text  string
	Style string
}

// Plain builds an unstyled cell.
func Plain(text string) Cell { return Cell{Text: text} }

// Styled builds a cell rendered with a semantic style.
func Styled(text, style string) Cell { return Cell{Text: text, Style: style} }

// width measures display columns, not bytes, so multi-byte resource names
// align.
//
// It must also ignore ANSI escape sequences: a cell can arrive already styled
// (the theme previews render in their own palette), and counting the escape
// bytes as visible characters pads the column to several times its real width.
func width(s string) int {
	if strings.Contains(s, "\x1b") {
		return lipgloss.Width(s)
	}
	return runewidth.StringWidth(s)
}

// pad aligns text to n display columns.
func pad(text string, n int, right bool) string {
	gap := n - width(text)
	if gap <= 0 {
		return text
	}
	spaces := strings.Repeat(" ", gap)
	if right {
		return spaces + text
	}
	return text + spaces
}

// Table renders a header row and body rows. Rows shorter than the column list
// are padded with empty cells, which is what the history and theme listings
// rely on for their blank marker column.
func (r *Renderer) Table(columns []Column, rows [][]Cell) {
	if len(columns) == 0 {
		return
	}

	widths := make([]int, len(columns))
	for i, column := range columns {
		widths[i] = width(column.Header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && width(cell.Text) > widths[i] {
				widths[i] = width(cell.Text)
			}
		}
	}

	// A flexed column gives back whatever the rest of the table overruns, so
	// long values are cut instead of wrapping across two lines.
	if flex := flexColumn(columns); flex >= 0 {
		total := 0
		for _, w := range widths {
			total += w + 2*len(cellPad)
		}
		if available := r.width(); total > available {
			shrunk := widths[flex] - (total - available)
			if shrunk < minFlexWidth {
				shrunk = minFlexWidth
			}
			if shrunk < widths[flex] {
				widths[flex] = shrunk
				for _, row := range rows {
					if flex < len(row) {
						row[flex].Text = ellipsize(row[flex].Text, shrunk)
					}
				}
			}
		}
	}

	var out strings.Builder

	// The header is styled as a whole, so a bold header stays bold across the
	// padding between columns exactly as Rich renders it.
	for i, column := range columns {
		out.WriteString(cellPad)
		out.WriteString(r.style(headerStyle, pad(column.Header, widths[i], column.Right)))
		out.WriteString(cellPad)
	}
	out.WriteString("\n")

	for _, row := range rows {
		for i := range columns {
			text, styleName := "", ""
			if i < len(row) {
				text, styleName = row[i].Text, row[i].Style
			}
			padded := pad(text, widths[i], columns[i].Right)
			out.WriteString(cellPad)
			if styleName == "" {
				out.WriteString(padded)
			} else {
				out.WriteString(r.style(styleName, padded))
			}
			out.WriteString(cellPad)
		}
		out.WriteString("\n")
	}

	r.write(out.String())
}

func flexColumn(columns []Column) int {
	for i, column := range columns {
		if column.Flex {
			return i
		}
	}
	return -1
}
