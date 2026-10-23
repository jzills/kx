package index

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// SortKey is one column a listing is sorted by, as --sort names it.
type SortKey struct {
	// Column is the name as typed, matched against a header by ColumnKey.
	Column     string
	Descending bool
}

// SortSpecError is a --sort value that names no column at all: empty, or a
// comma with nothing on one side of it.
type SortSpecError struct{ Spec string }

func (e SortSpecError) Error() string { return "'" + e.Spec + "' names no column" }

// UnknownColumnError is a sort key no table of the listing has a column for.
// Accepted is what the listing's headers can be typed as, in header order, so
// the refusal offers exactly the columns on screen — a CRD's, -o wide's.
type UnknownColumnError struct {
	Column   string
	Accepted []string
}

func (e UnknownColumnError) Error() string { return "no column '" + e.Column + "'" }

// ParseSortKeys reads a --sort value: column names separated by commas, each
// descending when it opens with "-". "status,-restarts" sorts by STATUS, then
// by RESTARTS from most to fewest.
func ParseSortKeys(spec string) ([]SortKey, error) {
	var keys []SortKey
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		descending := strings.HasPrefix(part, "-")
		column := strings.TrimSpace(strings.TrimPrefix(part, "-"))
		if column == "" {
			return nil, SortSpecError{Spec: spec}
		}
		keys = append(keys, SortKey{Column: column, Descending: descending})
	}
	return keys, nil
}

// ColumnKey is how a header is typed as a sort key: lowercase, with kubectl's
// unit note dropped and its word separators made one, so CPU(cores) is "cpu",
// MEMORY(bytes) "memory", and NOMINATED NODE "nominated-node" (or
// "nominated_node", or "nominated node" quoted). CPU% and MEM% keep their
// percent sign, which is what sets them apart from CPU and MEMORY.
func ColumnKey(name string) string {
	if open := strings.Index(name, "("); open > 0 {
		name = name[:open]
	}
	return strings.ToLower(separators.ReplaceAllString(strings.TrimSpace(name), "-"))
}

var separators = regexp.MustCompile(`[\s_-]+`)

// Sort orders each table of the listing by keys, stably, so rows the keys tie
// on keep the order kubectl gave them. A listing of several kinds is sorted
// table by table: their columns differ, so a key one table lacks leaves its
// rows to the keys it has. A key no table has is refused.
func (l Listing) Sort(keys []SortKey) (Listing, error) {
	if len(keys) == 0 {
		return l, nil
	}
	var accepted []string
	for _, key := range keys {
		found := false
		for _, section := range l.sections {
			if columnOf(section.shape.Headers, key.Column) >= 0 {
				found = true
			}
		}
		if !found {
			for _, section := range l.sections {
				accepted = appendKeys(accepted, section.shape.Headers)
			}
			return Listing{}, UnknownColumnError{Column: key.Column, Accepted: accepted}
		}
	}
	sorted := make([]section, len(l.sections))
	for i, section := range l.sections {
		sorted[i] = section
		sorted[i].rows = sortRows(section.shape.Headers, section.rows, keys)
	}
	return Listing{sections: sorted}, nil
}

// SortRows is Sort for one table already parsed into headers and rows, as kx
// top holds it once its percentage columns are added. A key the headers lack
// is refused.
func SortRows(headers []string, rows [][]string, keys []SortKey) ([][]string, error) {
	for _, key := range keys {
		if columnOf(headers, key.Column) < 0 {
			return nil, UnknownColumnError{Column: key.Column, Accepted: appendKeys(nil, headers)}
		}
	}
	return sortRows(headers, rows, keys), nil
}

func sortRows(headers []string, rows [][]string, keys []SortKey) [][]string {
	type column struct {
		at         int
		descending bool
		values     []cellValue
	}
	var columns []column
	for _, key := range keys {
		at := columnOf(headers, key.Column)
		if at < 0 {
			continue
		}
		columns = append(columns, column{at: at, descending: key.Descending, values: columnValues(rows, at)})
	}
	if len(columns) == 0 {
		return rows
	}
	// Sorted as positions, so each comparison reads the values parsed once
	// above rather than parsing two cells every time.
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		for _, c := range columns {
			if n := compareCells(c.values[a], c.values[b], c.descending); n != 0 {
				return n
			}
		}
		return 0
	})
	sorted := make([][]string, len(rows))
	for i, at := range order {
		sorted[i] = rows[at]
	}
	return sorted
}

func columnOf(headers []string, typed string) int {
	want := ColumnKey(typed)
	for i, header := range headers {
		if ColumnKey(header) == want {
			return i
		}
	}
	return -1
}

func appendKeys(keys []string, headers []string) []string {
	for _, header := range headers {
		if key := ColumnKey(header); !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// cellValue is a cell as it sorts: a number when its column reads as one kind
// of quantity throughout, its text otherwise, and missing for kubectl's
// placeholders, which sort last whichever way the column runs.
type cellValue struct {
	missing bool
	number  float64
	text    string
	numeric bool
}

func compareCells(a, b cellValue, descending bool) int {
	switch {
	case a.missing && b.missing:
		return 0
	case a.missing:
		return 1
	case b.missing:
		return -1
	}
	var n int
	if a.numeric {
		n = cmp.Compare(a.number, b.number)
	} else {
		n = strings.Compare(a.text, b.text)
	}
	if descending {
		return -n
	}
	return n
}

// missingCell is what kubectl prints where a value has none — <none>,
// <unknown> — and what kx prints in a column of its own: kx top's CPU% is "—"
// for a Pod with no limit. Read as text, one "—" turned the whole column to
// text, where 9% sorts above 50%.
func missingCell(cell string) bool {
	return cell == "" || cell == "—" || strings.HasPrefix(cell, "<") && strings.HasSuffix(cell, ">")
}

// columnValues reads a column as the first kind of value every one of its
// cells parses as. Decided over the whole column, not cell by cell, so one
// odd cell turns the column to text rather than sorting numbers among words.
// AGE reads as a duration, so 3h comes before 5d; RESTARTS as a count, its
// "(3m ago)" ignored; READY as a ratio; CPU and MEMORY as quantities; CPU% as
// a percentage.
func columnValues(rows [][]string, at int) []cellValue {
	cells := make([]string, len(rows))
	for i, row := range rows {
		if at < len(row) {
			cells[i] = strings.TrimSpace(row[at])
		}
	}
	for _, parse := range cellParsers {
		if values, ok := parseColumn(cells, parse); ok {
			return values
		}
	}
	values := make([]cellValue, len(cells))
	for i, cell := range cells {
		values[i] = cellValue{missing: missingCell(cell), text: cell}
	}
	return values
}

func parseColumn(cells []string, parse func(string) (float64, bool)) ([]cellValue, bool) {
	values := make([]cellValue, len(cells))
	for i, cell := range cells {
		if missingCell(cell) {
			values[i] = cellValue{missing: true}
			continue
		}
		number, ok := parse(cell)
		if !ok {
			return nil, false
		}
		values[i] = cellValue{number: number, numeric: true}
	}
	return values, true
}

// cellParsers are tried in order. A plain number comes first, since it would
// also read as a quantity; a duration before a quantity, since "5m" reads as
// both and kubectl prints AGE that way, while a CPU column of millicores
// sorts the same either way.
var cellParsers = []func(string) (float64, bool){
	parseCount, parsePercent, parseRatio, parseDuration, parseQuantity,
}

// count is a number, optionally followed by kubectl's note in parentheses:
// RESTARTS prints "5 (3m ago)".
var count = regexp.MustCompile(`^(\d+(?:\.\d+)?)(?: \(.*\))?$`)

func parseCount(cell string) (float64, bool) {
	match := count.FindStringSubmatch(cell)
	if match == nil {
		return 0, false
	}
	number, err := strconv.ParseFloat(match[1], 64)
	return number, err == nil
}

func parsePercent(cell string) (float64, bool) {
	if !strings.HasSuffix(cell, "%") {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimSuffix(cell, "%"), 64)
	return number, err == nil
}

var ratio = regexp.MustCompile(`^(\d+)/(\d+)$`)

// parseRatio reads READY: 0/1 before 1/2 before 1/1.
func parseRatio(cell string) (float64, bool) {
	match := ratio.FindStringSubmatch(cell)
	if match == nil {
		return 0, false
	}
	ready, _ := strconv.ParseFloat(match[1], 64)
	total, _ := strconv.ParseFloat(match[2], 64)
	if total == 0 {
		return ready, true
	}
	return ready / total, true
}

// duration is kubectl's human age: 45s, 10m, 3h12m, 5d, 2y45d.
var duration = regexp.MustCompile(`^(?:(\d+)y)?(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$`)

func parseDuration(cell string) (float64, bool) {
	match := duration.FindStringSubmatch(cell)
	if match == nil || cell == "" {
		return 0, false
	}
	seconds := 0.0
	for i, unit := range []float64{365 * 86400, 86400, 3600, 60, 1} {
		if match[i+1] != "" {
			n, _ := strconv.ParseFloat(match[i+1], 64)
			seconds += n * unit
		}
	}
	return seconds, true
}

// parseQuantity reads a Kubernetes quantity: 250m of CPU, 128Mi of memory.
func parseQuantity(cell string) (float64, bool) {
	quantity, err := resource.ParseQuantity(cell)
	if err != nil {
		return 0, false
	}
	return quantity.AsApproximateFloat64(), true
}
