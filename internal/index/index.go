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

// blankFirstColumn reports whether a data row leaves its first column empty,
// which whitespace splitting cannot otherwise see.
//
// `kubectl config get-contexts` is the case this exists for: it marks the
// active context in a leading CURRENT column that is blank on every other row.
// Trimming the line first makes that empty cell vanish and shifts every value
// one column left, so a non-current context's NAME reads as its CLUSTER — and
// because both rows then claim the same NAME, Add's dedupe drops one entirely.
//
// Only the *leading* cell is inferred, and only from the row's own indentation.
// That is deliberately narrower than reconstructing columns from the header's
// byte offsets, which this parser tried once and reverted: kubectl recomputes
// each --watch row's widths independently, so a value wider than the header saw
// gets sliced at a stale offset. Nothing here depends on any column's width.
// kubectl left-aligns, so a row that is blank where its first value belongs has
// no other reading.
func blankFirstColumn(line string) bool {
	rest := strings.TrimLeft(line, " \t")
	return rest != line && rest != ""
}

// Row splits one data line into exactly len(s.Headers) fields, padding with
// "" for a line shorter than the header (Python's slicing did this; Go
// indexing would panic without it).
func (s TableShape) Row(line string) []string {
	count := len(s.Headers)
	var cols []string
	if count > 1 && blankFirstColumn(line) {
		// The remainder holds one fewer field, so the cap shifts with it —
		// otherwise the last column stops absorbing its own trailing spaces.
		cols = append([]string{""}, splitColumns(line, count-1)...)
	} else {
		cols = splitColumns(line, count)
	}
	for len(cols) < count {
		cols = append(cols, "")
	}
	return cols
}

// parseTable splits kubectl table output into the header shape and its rows.
// Returns ok=false when the output isn't a table kx can index.
//
// Returns the whole TableShape rather than the three loose values the exported
// wrapper hands back, because Add needs NamespaceIdx as well and rebuilding the
// shape from a []string of headers would mean locating those columns twice.
func parseTable(output string) (shape TableShape, rows [][]string, ok bool) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return TableShape{}, nil, false
	}

	shape, ok = ParseHeader(lines[0])
	if !ok {
		return TableShape{}, nil, false
	}

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, shape.Row(line))
	}
	return shape, rows, true
}

// parseOutput splits kubectl table output into headers, rows and the position
// of the NAME column. Returns a nil header slice when the output isn't a table
// kx can index.
func parseOutput(output string) (headers []string, rows [][]string, nameIdx int) {
	shape, rows, ok := parseTable(output)
	if !ok {
		return nil, nil, 0
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

// Entry is one indexed row: the resource's name, and the namespace it was
// listed in when the table said so.
//
// Namespace is empty for the ordinary single-namespace listing, whose table has
// no NAMESPACE column and whose namespace the caller already knows. It is
// populated only for an all-namespace listing, where it is the sole thing
// distinguishing two rows that share a name.
type Entry struct {
	Name      string
	Namespace string
}

// Service prefixes kubectl output with an index column and filters it by name.
type Service struct{}

// Add prefixes an "X" index column to kubectl output and returns the indexed
// table alongside the entries it assigned, in index order.
func (Service) Add(output string) (string, []Entry) {
	shape, rows, ok := parseTable(output)
	if !ok {
		return output, nil
	}

	// Index numbers must map 1:1 to saved state, so a row that is
	// indistinguishable from an earlier one is collapsed (first-seen wins) —
	// otherwise the displayed indexes and the state entries desync.
	//
	// What "indistinguishable" means depends on the table. Name alone is right
	// for a single namespace, and wrong the moment a listing spans them: two
	// namespaces running the same workload name hold two different resources,
	// and collapsing them drops one from the table entirely. So the key takes
	// in the namespace whenever the table reports one.
	seen := make(map[Entry]bool, len(rows))
	unique := make([][]string, 0, len(rows))
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entry := Entry{Name: row[shape.NameIdx]}
		if shape.NamespaceIdx >= 0 {
			entry.Namespace = row[shape.NamespaceIdx]
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		unique = append(unique, row)
		entries = append(entries, entry)
	}

	allRows := make([][]string, 0, len(unique)+1)
	allRows = append(allRows, append([]string{"X"}, shape.Headers...))
	for i, row := range unique {
		allRows = append(allRows, append([]string{strconv.Itoa(i + 1)}, row...))
	}

	return Format(allRows), entries
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
