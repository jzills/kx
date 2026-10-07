// Package index turns kubectl table output into indexed output, and resolves a
// 1-based index back to the resource name it was assigned.
package index

import (
	"errors"
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

// ErrOutOfRange reports an index that names no row in the listing it was
// counted against.
//
// A sentinel rather than a sentence. This package sees an ordered list of
// names and nothing else — not the kind they are, not which of the three
// listings (the cursor's, a slot's, one the caller named a kind for) the
// caller is resolving against — so every message it could write is vaguer
// than the one the caller can write. It used to write "current state has 29
// items" while state's own two failures said "the current listing has 29
// Pods"; the wording lives with the facts now. Match with errors.Is.
var ErrOutOfRange = errors.New("index out of range")

// Resolve maps a 1-based index onto the nth resource name in state.
func Resolve(state Resolver, index int) (string, error) {
	names := state.Names()
	if index < 1 || index > len(names) {
		return "", ErrOutOfRange
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
	Headers []string
	NameIdx int
	// ResourceIdx is the column naming the resource an index resolves to,
	// which is not always NAME. See resourceIndex.
	ResourceIdx  int
	EventIdx     int
	NamespaceIdx int
}

// resourceIndex locates the column naming the resource an index addresses.
//
// Usually that is NAME, but `kubectl top pod --containers` prints one row per
// container: POD holds the pod and NAME holds the container inside it. An index
// has to resolve to something kx can act on — `kx logs 3` needs a pod, and no
// kubectl call takes a bare container name — so POD wins wherever both appear.
//
// Reading NAME there is what made `kx top --containers` save containers as
// pods, so `kx logs 1` looked up a pod named "istio-proxy" and reported it
// missing.
func resourceIndex(headers []string, nameIdx int) int {
	if pod := ColumnIndex(headers, "POD"); pod >= 0 {
		return pod
	}
	return nameIdx
}

// ParseHeader splits a kubectl header line into column names, and locates
// the NAME/EVENT/NAMESPACE columns. Returns ok=false for a header with no
// NAME column, the same "not indexable" signal parseOutput has always used.
//
// Exported so a caller streaming rows one at a time (kx get --watch) can
// parse the header once and split every following line the same way a
// complete table would through ParseTable.
func ParseHeader(header string) (TableShape, bool) {
	return shapeOf(splitColumns(header, -1))
}

// shapeOf locates the special columns in an already-split header.
//
// Split out from ParseHeader so a caller holding rows parsed further upstream
// can describe them without a header *line* to re-split — which is what lets
// the pipeline parse once and pass rows the rest of the way.
func shapeOf(headers []string) (TableShape, bool) {
	if len(headers) == 0 {
		return TableShape{}, false
	}
	nameIdx := ColumnIndex(headers, "NAME")
	if nameIdx < 0 {
		return TableShape{}, false
	}
	return TableShape{
		Headers:      headers,
		NameIdx:      nameIdx,
		ResourceIdx:  resourceIndex(headers, nameIdx),
		EventIdx:     ColumnIndex(headers, "EVENT"),
		NamespaceIdx: ColumnIndex(headers, "NAMESPACE"),
	}, true
}

// ColumnIndex reports the position of a named column, or -1 when the table has
// none.
//
// Exported because kx builds columns of its own on top of kubectl's — top adds
// CPU(%) and MEMORY(%) — and needs the same lookup for them. A local copy in
// internal/cli was byte-for-byte this function.
func ColumnIndex(headers []string, name string) int {
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
//
// A listing of several kinds is several tables; their rows come back together
// under the first one's shape, each split by its own. Callers that draw or
// number a listing read the tables apart through parseSections.
func parseTable(output string) (shape TableShape, rows [][]string, ok bool) {
	sections, ok := parseSections(output)
	if !ok {
		return TableShape{}, nil, false
	}
	for _, section := range sections {
		rows = append(rows, section.rows...)
	}
	return sections[0].shape, rows, true
}

// section is one table of kubectl's output: a header and the rows under it.
type section struct {
	shape TableShape
	rows  [][]string
}

// parseSections splits kubectl table output into its tables.
//
// kubectl prints one table per kind when a listing names several — `kubectl
// get all`, `kubectl get deploy,svc` — each under its own header, a blank line
// before each. Read as one table, the second header became a row: numbered,
// saved, and resolving to a resource called "NAME", while every later header
// was deduplicated away as another "NAME" and its rows were laid out under
// the first table's columns, a Service's TYPE under a Deployment's READY.
//
// A header is a line after a blank one that parses as a header. kubectl puts
// no blank line inside a table, so only a new table follows one.
func parseSections(output string) ([]section, bool) {
	lines := strings.Split(output, "\n")
	shape, ok := ParseHeader(lines[0])
	if !ok {
		return nil, false
	}
	sections := []section{{shape: shape}}
	afterBlank := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			afterBlank = true
			continue
		}
		if afterBlank {
			afterBlank = false
			if shape, ok := ParseHeader(line); ok {
				sections = append(sections, section{shape: shape})
				continue
			}
		}
		current := &sections[len(sections)-1]
		current.rows = append(current.rows, current.shape.Row(line))
	}
	return sections, true
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

// rowKey is what makes one row distinct from the rows before it.
//
// Deliberately not the Entry: several rows can resolve to one resource —
// `kubectl top pod --containers` prints a row per container, every one of them
// naming the same pod — and they are distinct rows for all that. Keying the
// dedupe on the Entry collapsed them.
type rowKey struct {
	Entry
	// Label is the NAME cell when NAME names something *inside* the resource
	// rather than the resource itself, and "" when the two are one column. It
	// is what holds two containers of the same pod apart.
	Label string
}

// Table is an indexed listing: the shape a renderer draws, the entries state
// resolves indexes against, and — for output kx cannot number — the raw text to
// print as it came.
//
// Rows are returned rather than only the padded text they would render as,
// because that text is a lossy encoding: an empty cell and column padding are
// the same run of spaces, so anything the parser recovered is destroyed the
// moment a second parser has to read it back. `kubectl config get-contexts`
// blanks its CURRENT column on every row but the active one, and that is
// exactly the cell that used to disappear between here and the screen.
type Table struct {
	Headers []string
	// Rows include the index column, so Headers[0] is "X" and Rows[n][0] is
	// the number.
	Rows    [][]string
	Entries []Entry
	// Raw is the untouched output, carried for the shapes kx cannot index —
	// JSON, YAML, a table with no NAME column.
	Raw string
	// Sections is the listing table by table when kubectl printed more than
	// one — a listing of several kinds — and nil otherwise. Each carries its
	// own header and its own rows, numbered on from the table before it.
	// Headers is then the first table's and Rows every table's, so a caller
	// counting rows or asking whether anything was found reads them as for
	// any listing; only drawing one needs the tables apart.
	Sections []Section
	// Match is the --match term the rows were narrowed by, empty for none.
	// It matters only once the narrowing leaves nothing: the caption then
	// names the term, since "none found" would say the namespace is empty
	// when it may be full of resources the term did not match.
	Match string
}

// Section is one table of a listing kubectl printed as several.
type Section struct {
	// Headers include "X", as Table.Headers does.
	Headers []string
	Rows    [][]string
}

// Indexable reports whether the output parsed as a table kx could number.
func (t Table) Indexable() bool { return t.Headers != nil }

// Placed reports whether the entries record where they live.
//
// Only meaningful for a listing that spans namespaces, where it is the
// difference between an index that resolves to one resource and an index that
// resolves to whichever namespace the caller is standing in. False for an
// ordinary single-namespace listing too — its table has no NAMESPACE column
// either — so callers ask this only when they know the scope is -A.
// Empty reports whether the table holds nothing to show — no indexable rows,
// and no raw output to print instead.
//
// Exported so a caller can tell an empty listing apart from a populated one
// without restating the test IndexedTable renders by: the two conditions used
// to be spelled in both places, and a caller deciding "was that listing
// empty?" for itself is how they drift.
func (t Table) Empty() bool {
	if !t.Indexable() {
		return strings.TrimSpace(t.Raw) == ""
	}
	return len(t.Rows) == 0
}

func (t Table) Placed() bool {
	for _, entry := range t.Entries {
		if entry.Namespace != "" {
			return true
		}
	}
	return false
}

// Text renders the table back to padded text. Non-tabular output comes back
// exactly as it arrived.
//
// Nothing in kx proper calls this — the pipeline hands rows all the way to the
// renderer — and nothing should start: padded text cannot represent an empty
// cell, so anything read back out of it has lost whatever the parser recovered.
// That is the round trip the Table type exists to close, and `kubectl config
// get-contexts` losing its blank CURRENT column is what it cost.
//
// It survives as a rendering of last resort for tests, which assert on the
// table a user would see. A production caller wanting text wants Rows.
func (t Table) Text() string {
	if !t.Indexable() {
		return t.Raw
	}
	if len(t.Sections) > 1 {
		tables := make([]string, 0, len(t.Sections))
		for _, section := range t.Sections {
			tables = append(tables, Format(append([][]string{section.Headers}, section.Rows...)))
		}
		return strings.Join(tables, "\n\n")
	}
	return Format(append([][]string{t.Headers}, t.Rows...))
}

// Service prefixes kubectl output with an index column and filters it by name.
type Service struct{}

// Add parses kubectl output and numbers it.
//
// A thin parse in front of AddRows, so the text and rows entry points cannot
// disagree about numbering, deduplication or which column is which.
func (s Service) Add(output string) Table {
	return s.AddMatching(output, "")
}

// AddMatching is Add narrowed to the rows whose name contains term, before
// anything is numbered, so the indexes run 1..n over the rows on screen. A
// table the term empties is dropped rather than drawn as a header over
// nothing. An empty term keeps every row.
func (s Service) AddMatching(output, term string) Table {
	listing, ok := ParseListing(output)
	if !ok {
		// Carried even here, where there are no rows for it to narrow: a
		// listing asked for with a term that found nothing at all is still
		// captioned with it, as every other empty listing with one is.
		return Table{Raw: output, Match: term}
	}
	table := listing.Narrow(term).Number()
	table.Raw = output
	table.Match = term
	return table
}

// RawTable is one of kubectl's tables as parsed, before anything is numbered:
// its header and the rows under it.
type RawTable struct {
	Headers []string
	Rows    [][]string
}

// ParseTables splits kubectl table output into its tables — one for a listing
// of one kind, one per kind for a listing of several — and reports false for
// output that is not a table.
//
// For a caller stitching several replies into one listing, which has to keep
// each kind's rows under that kind's own columns: ParseTable lays every
// table's rows under the first one's.
func ParseTables(output string) ([]RawTable, bool) {
	sections, ok := parseSections(output)
	if !ok {
		return nil, false
	}
	tables := make([]RawTable, 0, len(sections))
	for _, section := range sections {
		tables = append(tables, RawTable{Headers: section.shape.Headers, Rows: section.rows})
	}
	return tables, true
}

// AddTables numbers tables already parsed as one listing, as Add numbers the
// tables of one reply: the indexes run on from one into the next, and a table
// left with no rows is dropped. A table with no NAME column numbers nothing.
func (s Service) AddTables(tables []RawTable) Table {
	listing, ok := ListingOf(tables)
	if !ok {
		return Table{}
	}
	return listing.Number()
}

// Listing is kubectl table output parsed into its tables, and nothing more:
// not narrowed, not numbered. Each of those is a step of its own, taken in
// that order, so a listing kx prints without numbering is narrowed without
// ever passing through the numbering.
//
// The two were one step. Numbering collapses a row that repeats an earlier
// one — it has to, so that indexes stay one-to-one with saved state — and
// output kx narrowed by numbering it lost every row that read like another:
// kx get sa -A -o custom-columns=NAME:.metadata.name,UID:.metadata.uid -m
// default printed one ServiceAccount where kubectl listed eight.
type Listing struct {
	sections []section
}

// ParseListing parses kubectl table output, reporting false for output that
// is not a table: JSON, YAML, names, or a table with no NAME column.
func ParseListing(output string) (Listing, bool) {
	sections, ok := parseSections(output)
	if !ok {
		return Listing{}, false
	}
	return Listing{sections: sections}, true
}

// ListingOf is a Listing of tables already parsed — replies stitched together
// — reporting false for none, or for one with no NAME column.
func ListingOf(tables []RawTable) (Listing, bool) {
	if len(tables) == 0 {
		return Listing{}, false
	}
	sections := make([]section, 0, len(tables))
	for _, table := range tables {
		shape, ok := shapeOf(table.Headers)
		if !ok {
			return Listing{}, false
		}
		sections = append(sections, section{shape: shape, rows: table.Rows})
	}
	return Listing{sections: sections}, true
}

// Narrow keeps the rows whose name contains term, case-insensitively — every
// such row, however alike two of them read (see FilterRows). An empty term
// keeps them all.
func (l Listing) Narrow(term string) Listing {
	if term == "" {
		return l
	}
	narrowed := make([]section, len(l.sections))
	for i, section := range l.sections {
		narrowed[i] = section
		narrowed[i].rows = FilterRows(section.shape.Headers, section.rows, term)
	}
	return Listing{sections: narrowed}
}

// Empty reports whether no table holds a row.
func (l Listing) Empty() bool {
	for _, section := range l.sections {
		if len(section.rows) > 0 {
			return false
		}
	}
	return true
}

// Number numbers the listing's tables, dropping any with no rows rather than
// drawing a header over nothing. When none has rows the listing is an empty
// one under the first table's header, as one table filtered to nothing has
// always been.
func (l Listing) Number() Table {
	kept := l.withRows()
	if len(kept) == 0 {
		if len(l.sections) == 0 {
			return Table{}
		}
		kept = []section{{shape: l.sections[0].shape}}
	}
	if len(kept) == 1 {
		return Service{}.AddRows(kept[0].shape.Headers, kept[0].rows)
	}
	return addSections(kept)
}

// Unnumbered lays the listing out as kubectl printed it, without an index
// column: each table under its own header, a blank line between them, and a
// table with no rows dropped. Every row is kept — nothing here numbers, so
// nothing is collapsed.
func (l Listing) Unnumbered() string {
	kept := l.withRows()
	tables := make([]string, 0, len(kept))
	for _, section := range kept {
		tables = append(tables, Format(append([][]string{section.shape.Headers}, section.rows...)))
	}
	return strings.Join(tables, "\n\n")
}

// withRows is the listing's tables that hold a row.
func (l Listing) withRows() []section {
	kept := make([]section, 0, len(l.sections))
	for _, section := range l.sections {
		if len(section.rows) > 0 {
			kept = append(kept, section)
		}
	}
	return kept
}

// addSections numbers several tables as one listing: the indexes run on from
// one table into the next, as the saved entry they resolve against does.
func addSections(sections []section) Table {
	var numbering numberer
	table := Table{Sections: make([]Section, 0, len(sections))}
	for _, section := range sections {
		rows := numbering.add(section.shape, section.rows)
		table.Sections = append(table.Sections, Section{
			Headers: append([]string{"X"}, section.shape.Headers...), Rows: rows,
		})
		table.Rows = append(table.Rows, rows...)
	}
	table.Headers = table.Sections[0].Headers
	table.Entries = numbering.entries
	return table
}

// AddRows prefixes an "X" index column to rows parsed upstream and returns the
// indexed table: its rows, and the entries it assigned in index order.
//
// Takes rows rather than text because the pipeline between kubectl and the
// screen parses once. Handing the next stage padded text meant it had to parse
// again, and padded text cannot represent an empty cell — its gap is
// indistinguishable from column padding.
func (Service) AddRows(headers []string, rows [][]string) Table {
	shape, ok := shapeOf(headers)
	if !ok {
		return Table{}
	}
	var numbering numberer
	indexed := numbering.add(shape, rows)
	return Table{
		Headers: append([]string{"X"}, shape.Headers...),
		Rows:    indexed,
		Entries: numbering.entries,
	}
}

// numberer assigns indexes across one or more tables, collapsing a row that
// repeats an earlier one.
type numberer struct {
	seen    map[rowKey]bool
	entries []Entry
}

// add numbers rows of one shape, continuing from whatever was numbered before.
func (n *numberer) add(shape TableShape, rows [][]string) [][]string {
	if n.seen == nil {
		n.seen = make(map[rowKey]bool, len(rows))
	}

	// Index numbers must map 1:1 to saved state, so a row that is
	// indistinguishable from an earlier one is collapsed (first-seen wins) —
	// otherwise the displayed indexes and the state entries desync.
	//
	// What "indistinguishable" means depends on the table, and it is a property
	// of the row, not of the resource the row resolves to. Name alone is right
	// for a single namespace, and wrong the moment a listing spans them: two
	// namespaces running the same workload name hold two different resources.
	// And a table can legitimately hold several rows for one resource —
	// `kubectl top pod --containers` prints one per container — which are
	// different rows however identical their Entry is. Collapsing on the Entry
	// dropped every container after the first, rendering six rows as three.
	indexed := make([][]string, 0, len(rows))
	for _, row := range rows {
		entry := Entry{Name: row[shape.ResourceIdx]}
		if shape.NamespaceIdx >= 0 {
			entry.Namespace = row[shape.NamespaceIdx]
		}
		key := rowKey{Entry: entry}
		if shape.ResourceIdx != shape.NameIdx {
			key.Label = row[shape.NameIdx]
		}
		if n.seen[key] {
			continue
		}
		n.seen[key] = true
		n.entries = append(n.entries, entry)
		indexed = append(indexed, append([]string{strconv.Itoa(len(n.entries))}, row...))
	}
	return indexed
}

// MatchesName reports whether name contains term, case-insensitively — what
// --match means everywhere it is accepted. An empty term matches everything,
// so a caller with no term can pass it straight through.
//
// One definition, so kx get -m and the sweeps' -m can never disagree about
// which names a term selects.
func MatchesName(name, term string) bool {
	return NameMatcher(term)(name)
}

// NameMatcher is MatchesName with the term fixed, for a caller testing one
// term against many names: the term is lowercased once, where MatchesName in
// a loop lowercased it for every name.
func NameMatcher(term string) func(name string) bool {
	term = strings.ToLower(term)
	return func(name string) bool {
		return strings.Contains(strings.ToLower(name), term)
	}
}

// FilterRows keeps the rows whose name contains term, case-insensitively.
//
// The name of what an index resolves to (TableShape.ResourceIdx): NAME, but
// POD under kubectl top pod --containers, whose NAME is the container. A term
// read off NAME there selected containers under a caption about pods, and
// missed the pods it named.
//
// The name, not the kind kubectl puts in front of it in a listing of several
// kinds: "app" is in every "deployment.apps/…". A name never holds a "/", so
// whatever precedes the last one is that prefix.
func FilterRows(headers []string, rows [][]string, term string) [][]string {
	shape, ok := shapeOf(headers)
	if !ok {
		return rows
	}
	matches := NameMatcher(term)
	kept := make([][]string, 0, len(rows))
	for _, row := range rows {
		name := row[shape.ResourceIdx]
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			name = name[slash+1:]
		}
		if matches(name) {
			kept = append(kept, row)
		}
	}
	return kept
}

// Resolve maps a 1-based index onto a resource name in state.
func (Service) Resolve(state Resolver, index int) (string, error) {
	return Resolve(state, index)
}
