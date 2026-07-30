// Package index turns kubectl table output into indexed output, and resolves a
// 1-based index back to the resource name it was assigned.
package index

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Resolver is the subset of the state a resolve needs: the ordered resource
// names an index counts against.
type Resolver interface {
	Names() []string
}

// Resolve maps a 1-based index onto the nth resource name in state.
func Resolve(state Resolver, index int) (string, error) {
	names := state.Names()
	count := len(names)
	if index < 1 || index > count {
		label := "items"
		if count == 1 {
			label = "item"
		}
		return "", fmt.Errorf(
			"Index %d is out of range — current state has %d %s (run 'kx state' to view).",
			index, count, label,
		)
	}
	return names[index-1], nil
}

// span is a half-open column range in the header line. end < 0 means
// end-of-line.
type span struct {
	start, end int
}

var columnRE = regexp.MustCompile(`\S+\s*`)

// slice extracts a span from a row, tolerating rows shorter than the header
// (Python's slicing yields "" past the end; Go would panic).
func (s span) slice(row string) string {
	if s.start >= len(row) {
		return ""
	}
	end := s.end
	if end < 0 || end > len(row) {
		end = len(row)
	}
	return strings.TrimSpace(row[s.start:end])
}

// parseOutput splits kubectl table output into headers, rows and the position
// of the NAME column. Returns a nil header slice when the output isn't a table
// kx can index.
func parseOutput(output string) (headers []string, rows [][]string, nameIdx int) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, nil, 0
	}

	header := lines[0]
	matches := columnRE.FindAllStringIndex(header, -1)
	spans := make([]span, 0, len(matches))
	for _, m := range matches {
		spans = append(spans, span{start: m[0], end: m[1]})
	}
	if len(spans) == 0 {
		return nil, nil, 0
	}
	// kubectl doesn't pad a table's last column with trailing spaces, so its
	// span (derived from the header word's own width) can be narrower than a
	// data row's actual value in that column. Extend it to end-of-line so
	// wider values aren't sliced off.
	spans[len(spans)-1].end = -1

	for _, s := range spans {
		headers = append(headers, s.slice(header))
	}

	nameIdx = -1
	for i, h := range headers {
		if h == "NAME" {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return nil, nil, 0
	}

	for _, row := range lines[1:] {
		if strings.TrimSpace(row) == "" {
			continue
		}
		cols := make([]string, 0, len(spans))
		for _, s := range spans {
			cols = append(cols, s.slice(row))
		}
		rows = append(rows, cols)
	}
	return headers, rows, nameIdx
}

// cellWidth measures a cell in terminal columns rather than bytes, matching
// what internal/render does for the tables it draws itself. Identical to len()
// for the ASCII kubectl emits for every built-in resource, so the layout the
// tests pin is unchanged; it only stops a non-ASCII value in a custom column
// from being padded several columns too wide.
func cellWidth(cell string) int { return runewidth.StringWidth(cell) }

// Format lays out rows as a left-aligned, two-space-separated table. Every
// cell is padded, including the last in a row, matching the Python
// implementation byte-for-byte.
//
// Exported for the same reason ParseTable is: `kx top` appends its own
// percentage columns and has to lay them out identically to the listing they
// extend, and a second copy of this would drift from it.
func Format(allRows [][]string) string {
	if len(allRows) == 0 {
		return ""
	}
	widths := make([]int, len(allRows[0]))
	for _, row := range allRows {
		for i, cell := range row {
			if w := cellWidth(cell); i < len(widths) && w > widths[i] {
				widths[i] = w
			}
		}
	}
	lines := make([]string, 0, len(allRows))
	for _, row := range allRows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = cell + strings.Repeat(" ", widths[i]-cellWidth(cell))
		}
		lines = append(lines, strings.Join(cells, "  "))
	}
	return strings.Join(lines, "\n")
}

// ParseTable splits kubectl table output into headers and rows, returning a nil
// header slice for anything kx can't index (JSON/YAML, or a table with no NAME
// column).
//
// Exported so the renderer shares this one parser rather than keeping a private
// copy: it extends the last column to end-of-line, so a value wider than its
// header is never sliced off, and a second implementation would drift from that.
func ParseTable(output string) (headers []string, rows [][]string, nameIdx int) {
	return parseOutput(output)
}

// CountRows reports how many resource rows kubectl output contains, and
// whether it is a table kx can index at all.
func CountRows(output string) (count int, tabular bool) {
	headers, rows, _ := parseOutput(output)
	if headers == nil {
		return 0, false
	}
	return len(rows), true
}

// Service prefixes kubectl output with an index column and filters it by name.
type Service struct{}

// Add prefixes an "X" index column to kubectl output and returns the indexed
// table alongside the resource names, in index order.
func (Service) Add(output string) (string, []string) {
	headers, rows, nameIdx := parseOutput(output)
	if headers == nil {
		return output, nil
	}

	// Index numbers must map 1:1 to saved state, which is keyed by name.
	// Collapse any repeated NAME (first-seen wins) so the displayed indexes
	// and the state entries can never desync.
	seen := make(map[string]bool, len(rows))
	unique := make([][]string, 0, len(rows))
	for _, row := range rows {
		if seen[row[nameIdx]] {
			continue
		}
		seen[row[nameIdx]] = true
		unique = append(unique, row)
	}

	names := make([]string, 0, len(unique))
	allRows := make([][]string, 0, len(unique)+1)
	allRows = append(allRows, append([]string{"X"}, headers...))
	for i, row := range unique {
		names = append(names, row[nameIdx])
		allRows = append(allRows, append([]string{strconv.Itoa(i + 1)}, row...))
	}

	return Format(allRows), names
}

// Filter drops rows whose NAME doesn't contain term, case-insensitively.
func (Service) Filter(output, term string) string {
	headers, rows, nameIdx := parseOutput(output)
	if headers == nil {
		return output
	}

	lower := strings.ToLower(term)
	allRows := [][]string{headers}
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row[nameIdx]), lower) {
			allRows = append(allRows, row)
		}
	}
	if len(allRows) == 1 {
		// Nothing matched: show the original header line untouched rather than
		// a re-padded one.
		return strings.Split(output, "\n")[0]
	}
	return Format(allRows)
}

// Resolve maps a 1-based index onto a resource name in state.
func (Service) Resolve(state Resolver, index int) (string, error) {
	return Resolve(state, index)
}
