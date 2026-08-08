// Package index turns kubectl table output into indexed output, and resolves a
// 1-based index back to the resource name it was assigned.
package index

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
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

// columnSepRE matches kubectl's real column separator: a run of two or more
// spaces. A value's own internal spacing ("13 (5h59m ago)") is always
// single, so splitting on 2+-space runs stays correct per row, unlike
// slicing at fixed byte offsets derived once from the header: kubectl's
// `--watch` printer recomputes each row's own column widths independently
// rather than keeping them pinned to the header, so a later value wider
// than anything the header saw (STATUS going from "Running" to
// "Terminating") used to get sliced at the stale offset and spill into the
// next column.
var columnSepRE = regexp.MustCompile(`\s{2,}`)

// TableShape is a parsed kubectl table header: column names and the column
// indexes of NAME, EVENT and NAMESPACE (-1 if absent — EVENT is only
// present when --output-watch-events was requested, NAMESPACE only when
// -A/--all-namespaces was).
type TableShape struct {
	Headers      []string
	NameIdx      int
	EventIdx     int
	NamespaceIdx int
}

// ParseHeader splits a kubectl header line into column names, and locates
// the NAME/EVENT/NAMESPACE columns. Returns ok=false for a header with no
// NAME column, the same "not indexable" signal parseOutput has always used.
//
// Exported so a caller streaming rows one at a time (kx get --watch) can
// parse the header once and split every following line the same way a
// complete table would through ParseTable.
func ParseHeader(header string) (TableShape, bool) {
	headers := splitColumns(header, -1)
	if len(headers) == 0 {
		return TableShape{}, false
	}

	nameIdx := columnIndex(headers, "NAME")
	if nameIdx < 0 {
		return TableShape{}, false
	}

	return TableShape{
		Headers:      headers,
		NameIdx:      nameIdx,
		EventIdx:     columnIndex(headers, "EVENT"),
		NamespaceIdx: columnIndex(headers, "NAMESPACE"),
	}, true
}

func columnIndex(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

// splitColumns splits a table line (header or data row) on runs of 2+
// spaces. n caps the number of pieces the way regexp.Split defines it: the
// last piece is the unsplit remainder, so a value that legitimately
// contains its own 2+-space run (unseen in practice, but this is the
// fallback) still lands whole in the last column rather than being cut
// further. n < 0 is uncapped, used for the header itself.
func splitColumns(line string, n int) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	return columnSepRE.Split(trimmed, n)
}

// Row splits one data line into exactly len(s.Headers) fields, padding with
// "" for a line shorter than the header (Python's slicing did this; Go
// indexing would panic without it).
func (s TableShape) Row(line string) []string {
	cols := splitColumns(line, len(s.Headers))
	for len(cols) < len(s.Headers) {
		cols = append(cols, "")
	}
	return cols
}

// parseOutput splits kubectl table output into headers, rows and the position
// of the NAME column. Returns a nil header slice when the output isn't a table
// kx can index.
func parseOutput(output string) (headers []string, rows [][]string, nameIdx int) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, nil, 0
	}

	shape, ok := ParseHeader(lines[0])
	if !ok {
		return nil, nil, 0
	}

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, shape.Row(line))
	}
	return shape.Headers, rows, shape.NameIdx
}

// cellWidth measures a cell the way kubectl's own table printer does — in
// runes, via text/tabwriter — so that a table Format lays out parses back
// through parseOutput with its columns in the same places.
//
// Deliberately not terminal width: "日本語" is three runes and six columns, and
// kubectl pads it to three. parseOutput reads both kubectl's output and
// Format's, so the two have to agree, and kubectl is the one that cannot be
// changed. What the user actually sees is laid out by render.Table, which does
// measure in terminal columns.
func cellWidth(cell string) int { return utf8.RuneCountInString(cell) }

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
