package index

import (
	"strings"
	"testing"
)

// Realistic kubectl output — column spans are load-bearing; keep alignment exact.
const podsOutput = "NAME             READY   STATUS    RESTARTS   AGE\n" +
	"nginx-abc-xyz    1/1     Running   0          5d\n" +
	"redis-def-uvw    1/1     Running   0          3d"

const singleRowOutput = "NAME             READY   STATUS    RESTARTS   AGE\n" +
	"only-pod-abc     1/1     Running   0          1d"

// `kubectl config get-contexts`-style output: the trailing NAMESPACE column has
// no header padding (kubectl doesn't pad a table's last column), but a data
// value in that column ("diagnostics") is wider than the header word itself.
const contextsOutput = "CURRENT   NAME             CLUSTER          AUTHINFO         NAMESPACE\n" +
	"*         docker-desktop   docker-desktop   docker-desktop   diagnostics"

// The same command with more than one context, which is where the marker column
// shows its real shape: it is blank on every row but the active one.
const twoContextsOutput = "CURRENT   NAME             CLUSTER          AUTHINFO         NAMESPACE\n" +
	"          alt              docker-desktop   docker-desktop   default\n" +
	"*         docker-desktop   docker-desktop   docker-desktop   diagnostics"

func TestParseOutputHeaders(t *testing.T) {
	headers, rows, nameIdx := parseOutput(podsOutput)
	want := []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}
	if len(headers) != len(want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Errorf("headers[%d] = %q, want %q", i, headers[i], want[i])
		}
	}
	if len(rows) != 2 {
		t.Errorf("len(rows) = %d, want 2", len(rows))
	}
	if nameIdx != 0 {
		t.Errorf("nameIdx = %d, want 0", nameIdx)
	}
	if rows[0][0] != "nginx-abc-xyz" || rows[1][0] != "redis-def-uvw" {
		t.Errorf("row names = %q, %q", rows[0][0], rows[1][0])
	}
}

func TestParseOutputEmpty(t *testing.T) {
	headers, rows, nameIdx := parseOutput("")
	if headers != nil || rows != nil || nameIdx != 0 {
		t.Errorf("parseOutput(\"\") = %v, %v, %d; want nil, nil, 0", headers, rows, nameIdx)
	}
}

func TestParseOutputHeaderOnly(t *testing.T) {
	headers, rows, _ := parseOutput("NAME             READY   STATUS    RESTARTS   AGE")
	if len(headers) != 5 {
		t.Errorf("len(headers) = %d, want 5", len(headers))
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0", len(rows))
	}
}

// kubectl doesn't pad the last column, so its value can be wider than the
// header word. Slicing on the header's own width would truncate it.
func TestParseOutputLastColumnNotTruncated(t *testing.T) {
	_, rows, _ := parseOutput(contextsOutput)
	if got := rows[0][len(rows[0])-1]; got != "diagnostics" {
		t.Errorf("last column = %q, want %q", got, "diagnostics")
	}
}

func TestParseOutputNoNameColumn(t *testing.T) {
	headers, _, _ := parseOutput("FOO   BAR\nbaz   qux")
	if headers != nil {
		t.Errorf("headers = %v, want nil for output with no NAME column", headers)
	}
}

func TestParseHeaderLocatesColumns(t *testing.T) {
	shape, ok := ParseHeader("NAME             READY   STATUS    RESTARTS   AGE")
	if !ok {
		t.Fatal("ParseHeader returned ok=false for a valid header")
	}
	want := []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}
	if len(shape.Headers) != len(want) {
		t.Fatalf("Headers = %v, want %v", shape.Headers, want)
	}
	for i, h := range want {
		if shape.Headers[i] != h {
			t.Errorf("Headers[%d] = %q, want %q", i, shape.Headers[i], h)
		}
	}
	if shape.NameIdx != 0 {
		t.Errorf("NameIdx = %d, want 0", shape.NameIdx)
	}
	if shape.EventIdx != -1 {
		t.Errorf("EventIdx = %d, want -1 (no EVENT column)", shape.EventIdx)
	}
}

func TestParseHeaderLocatesEventColumn(t *testing.T) {
	shape, ok := ParseHeader("EVENT      NAME                 STATUS   AGE")
	if !ok {
		t.Fatal("ParseHeader returned ok=false")
	}
	if shape.EventIdx != 0 {
		t.Errorf("EventIdx = %d, want 0", shape.EventIdx)
	}
	if shape.NameIdx != 1 {
		t.Errorf("NameIdx = %d, want 1", shape.NameIdx)
	}
}

func TestParseHeaderNoNameColumnReturnsFalse(t *testing.T) {
	if _, ok := ParseHeader("FOO   BAR"); ok {
		t.Error("ParseHeader returned ok=true for a header with no NAME column")
	}
}

func TestParseHeaderLocatesNamespaceColumn(t *testing.T) {
	shape, ok := ParseHeader("EVENT      NAMESPACE   NAME             STATUS")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	if shape.NamespaceIdx != 1 {
		t.Errorf("NamespaceIdx = %d, want 1", shape.NamespaceIdx)
	}
}

func TestParseHeaderNoNamespaceColumnReturnsNegativeOne(t *testing.T) {
	shape, ok := ParseHeader("NAME             STATUS")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	if shape.NamespaceIdx != -1 {
		t.Errorf("NamespaceIdx = %d, want -1", shape.NamespaceIdx)
	}
}

func TestTableShapeRowSlicesLikeParseTable(t *testing.T) {
	shape, ok := ParseHeader("NAME             READY   STATUS    RESTARTS   AGE")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	got := shape.Row("nginx-abc-xyz    1/1     Running   0          5d")
	want := []string{"nginx-abc-xyz", "1/1", "Running", "0", "5d"}
	if len(got) != len(want) {
		t.Fatalf("Row = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Row[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A value wider than its header must not be sliced off — TableShape.Row
// extends the last column to end-of-line, same as ParseTable.
// Captured live from `kubectl get pods --watch --output-watch-events`: the
// header was sized for "Running" (7 chars), but a later MODIFIED row's
// STATUS value, "Terminating" (11 chars), is wider than the header assumed
// — kubectl recomputes each watch row's own column widths independently, it
// doesn't keep them pinned to the header the way a one-shot table does.
// Slicing at the header's fixed byte offset used to cut STATUS short and
// spill its tail into RESTARTS.
const watchHeaderReal = "EVENT      NAME                        READY   STATUS    RESTARTS   AGE"
const watchAddedRunningReal = "ADDED      waypoint-5d84f566ff-hb8rk   1/1     Running   0          106s"
const watchModifiedTerminatingReal = "MODIFIED   waypoint-5d84f566ff-hb8rk   1/1     Terminating   0          107s"

func TestTableShapeRowHandlesColumnWidthDriftAcrossWatchEvents(t *testing.T) {
	shape, ok := ParseHeader(watchHeaderReal)
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}

	got := shape.Row(watchModifiedTerminatingReal)
	want := []string{"MODIFIED", "waypoint-5d84f566ff-hb8rk", "1/1", "Terminating", "0", "107s"}
	if len(got) != len(want) {
		t.Fatalf("Row = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Row[%d] = %q, want %q (full row: %#v)", i, got[i], want[i], got)
		}
	}
}

func TestTableShapeRowStillHandlesTheNarrowerRow(t *testing.T) {
	shape, ok := ParseHeader(watchHeaderReal)
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	got := shape.Row(watchAddedRunningReal)
	want := []string{"ADDED", "waypoint-5d84f566ff-hb8rk", "1/1", "Running", "0", "106s"}
	if len(got) != len(want) {
		t.Fatalf("Row = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Row[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTableShapeRowLastColumnNotTruncated(t *testing.T) {
	shape, ok := ParseHeader("NAME   AGE")
	if !ok {
		t.Fatal("ParseHeader: ok=false")
	}
	got := shape.Row("nginx  1000000000000000d")
	if got[1] != "1000000000000000d" {
		t.Errorf("Row[1] = %q, want the full unsliced value", got[1])
	}
}

func TestAddPrependsIndexColumn(t *testing.T) {
	namesTable := Service{}.Add(podsOutput)
	output, names := namesTable.Text(), namesTable.Entries
	lines := strings.Split(output, "\n")
	if !strings.HasPrefix(lines[0], "X") {
		t.Errorf("header = %q, want it to start with X", lines[0])
	}
	if !strings.HasPrefix(lines[1], "1") || !strings.HasPrefix(lines[2], "2") {
		t.Errorf("indexes are not 1-based: %q, %q", lines[1], lines[2])
	}
	want := []string{"nginx-abc-xyz", "redis-def-uvw"}
	if len(names) != 2 || names[0].Name != want[0] || names[1].Name != want[1] {
		t.Errorf("names = %+v, want %v", names, want)
	}
}

func TestAddSingleRow(t *testing.T) {
	namesTable := Service{}.Add(singleRowOutput)
	_, names := namesTable.Text(), namesTable.Entries
	if len(names) != 1 || names[0].Name != "only-pod-abc" {
		t.Errorf("names = %v, want [only-pod-abc]", names)
	}
}

func TestAddEmptyOutputReturnsOriginal(t *testing.T) {
	namesTable := Service{}.Add("")
	output, names := namesTable.Text(), namesTable.Entries
	if output != "" || names != nil {
		t.Errorf("Add(\"\") = %q, %v; want \"\", nil", output, names)
	}
}

// Non-tabular payloads must pass through untouched rather than being mangled
// into a table.
func TestAddNonTabularOutputReturnsRaw(t *testing.T) {
	for _, raw := range []string{
		`{"apiVersion": "v1", "kind": "List"}`,
		"apiVersion: v1\nkind: List\n",
	} {
		namesTable := Service{}.Add(raw)
		output, names := namesTable.Text(), namesTable.Entries
		if output != raw || names != nil {
			t.Errorf("Add(%q) = %q, %v; want unchanged", raw, output, names)
		}
	}
}

func TestAddLastColumnNotTruncated(t *testing.T) {
	_Table := Service{}.Add(contextsOutput)
	output, _ := _Table.Text(), _Table.Entries
	if !strings.Contains(output, "diagnostics") {
		t.Errorf("indexed output dropped the wide last column:\n%s", output)
	}
}

// Index numbers map 1:1 to saved state, which is keyed by name — a repeated
// NAME would desync the displayed indexes from the state entries.
func TestAddDedupesDuplicateNamesKeepingFirst(t *testing.T) {
	duplicated := "NAME      READY   STATUS\n" +
		"pod-a     1/1     Running\n" +
		"pod-a     0/1     Pending\n" +
		"pod-b     1/1     Running"
	namesTable := Service{}.Add(duplicated)
	output, names := namesTable.Text(), namesTable.Entries
	if len(names) != 2 || names[0].Name != "pod-a" || names[1].Name != "pod-b" {
		t.Fatalf("names = %v, want [pod-a pod-b]", names)
	}
	if strings.Contains(output, "Pending") {
		t.Errorf("kept the duplicate row instead of the first:\n%s", output)
	}
}

func TestFilterMatching(t *testing.T) {
	output := Service{}.Filter(podsOutput, "nginx")
	if !strings.Contains(output, "nginx-abc-xyz") {
		t.Errorf("filter dropped the matching row:\n%s", output)
	}
	if strings.Contains(output, "redis-def-uvw") {
		t.Errorf("filter kept a non-matching row:\n%s", output)
	}
	if !strings.HasPrefix(output, "NAME") {
		t.Errorf("filter dropped the header:\n%s", output)
	}
}

func TestFilterCaseInsensitive(t *testing.T) {
	output := Service{}.Filter(podsOutput, "NGINX")
	if !strings.Contains(output, "nginx-abc-xyz") {
		t.Errorf("filter is case-sensitive:\n%s", output)
	}
}

func TestFilterNoMatchReturnsHeaderOnly(t *testing.T) {
	output := Service{}.Filter(podsOutput, "nothing-matches")
	if output != strings.Split(podsOutput, "\n")[0] {
		t.Errorf("no-match filter = %q, want the original header line", output)
	}
}

func TestFilterLastColumnNotTruncated(t *testing.T) {
	output := Service{}.Filter(contextsOutput, "docker")
	if !strings.Contains(output, "diagnostics") {
		t.Errorf("filter truncated the wide last column:\n%s", output)
	}
}

type fakeResolver struct{ names []string }

func (f fakeResolver) Names() []string { return f.names }

func TestResolveValidIndexes(t *testing.T) {
	state := fakeResolver{names: []string{"a", "b", "c"}}
	for index, want := range map[int]string{1: "a", 2: "b", 3: "c"} {
		got, err := Resolve(state, index)
		if err != nil {
			t.Fatalf("Resolve(%d) errored: %v", index, err)
		}
		if got != want {
			t.Errorf("Resolve(%d) = %q, want %q", index, got, want)
		}
	}
}

func TestResolveOutOfRange(t *testing.T) {
	state := fakeResolver{names: []string{"a", "b"}}
	for _, index := range []int{0, 3, -1} {
		if _, err := Resolve(state, index); err == nil {
			t.Errorf("Resolve(%d) succeeded, want an out-of-range error", index)
		}
	}
}

// The message is asserted because it is the user-facing contract, and it
// singularizes on a one-item state.
func TestResolveOutOfRangeMessage(t *testing.T) {
	_, err := Resolve(fakeResolver{names: []string{"only"}}, 5)
	want := "Index 5 is out of range — current state has 1 item (run 'kx state' to view)."
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestCountRows(t *testing.T) {
	if count, tabular := CountRows(podsOutput); count != 2 || !tabular {
		t.Errorf("CountRows(pods) = %d, %v; want 2, true", count, tabular)
	}
	if count, tabular := CountRows(`{"kind":"List"}`); count != 0 || tabular {
		t.Errorf("CountRows(json) = %d, %v; want 0, false", count, tabular)
	}
}

// Padding is measured in runes, which is the unit kubectl's own table printer
// uses (text/tabwriter). Both kubectl's output and Format's flow into
// parseOutput, so the two have to agree on where a column starts.
//
// Not terminal width: "日本語" is three runes and six columns. kubectl pads it
// to three, so kx must too, or a round trip through parseOutput cuts the
// columns after it in the wrong place. What the user sees is laid out by
// render.Table, which does measure in terminal columns.
func TestFormatPadsByRuneCount(t *testing.T) {
	out := Format([][]string{
		{"NAME", "STATUS"},
		{"日本語", "Running"},
		{"web", "Running"},
	})
	seen := map[int][]string{}
	for _, line := range strings.Split(out, "\n") {
		seen[len([]rune(line))] = append(seen[len([]rune(line))], line)
	}
	if len(seen) != 1 {
		t.Errorf("rows have %d different rune counts, want 1:\n%s\n%v", len(seen), out, seen)
	}
}

// ASCII is all kubectl emits for built-in resources, so the layout every other
// test pins must be untouched.
func TestFormatIsUnchangedForASCII(t *testing.T) {
	rows := [][]string{{"X", "NAME", "AGE"}, {"1", "web", "5d"}, {"2", "redis-longer", "3d"}}
	want := "X  NAME          AGE\n1  web           5d \n2  redis-longer  3d "
	if got := Format(rows); got != want {
		t.Errorf("Format =\n%q\nwant\n%q", got, want)
	}
}

// The real defect #160 was filed for: a multi-byte value shifts every later
// column's byte offset, so byte slicing cut them in the wrong place. This is
// kubectl's actual layout, captured from
// `kubectl get cm -o custom-columns=NAME:...,NOTE:...,KIND:.kind` with a CJK
// annotation — note that KIND begins at rune 30 on every row, byte 30 on the
// ASCII rows and byte 36 on the CJK one.
func TestParseTableHandlesMultiByteValues(t *testing.T) {
	output := "NAME                 NOTE     KIND\n" +
		"alpha                日本語      ConfigMap\n" +
		"beta                 abcdef   ConfigMap"

	headers, rows, nameIdx := ParseTable(output)
	if headers == nil {
		t.Fatal("ParseTable returned no headers")
	}
	if got := strings.Join(headers, "|"); got != "NAME|NOTE|KIND" {
		t.Fatalf("headers = %q", got)
	}
	if nameIdx != 0 {
		t.Errorf("nameIdx = %d, want 0", nameIdx)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for i, want := range [][]string{
		{"alpha", "日本語", "ConfigMap"},
		{"beta", "abcdef", "ConfigMap"},
	} {
		if got := strings.Join(rows[i], "|"); got != strings.Join(want, "|") {
			t.Errorf("row %d = %q, want %q", i, got, strings.Join(want, "|"))
		}
	}
}

// Add indexes that same table and Format re-emits it; render then parses the
// result back. The round trip has to survive a multi-byte value, or the
// listing loses a column between being built and being drawn.
func TestIndexedMultiByteTableRoundTrips(t *testing.T) {
	output := "NAME                 NOTE     KIND\n" +
		"alpha                日本語      ConfigMap\n" +
		"beta                 abcdef   ConfigMap"

	entriesTable := Service{}.Add(output)
	indexed, entries := entriesTable.Text(), entriesTable.Entries
	if len(entries) != 2 || entries[0].Name != "alpha" || entries[1].Name != "beta" {
		t.Fatalf("entries = %+v, want alpha then beta", entries)
	}
	headers, rows, _ := ParseTable(indexed)
	if got := strings.Join(headers, "|"); got != "X|NAME|NOTE|KIND" {
		t.Fatalf("re-parsed headers = %q\n%s", got, indexed)
	}
	if got := strings.Join(rows[0], "|"); got != "1|alpha|日本語|ConfigMap" {
		t.Errorf("re-parsed row = %q, want 1|alpha|日本語|ConfigMap\n%s", got, indexed)
	}
}

// `kubectl config get-contexts` marks the active context in a leading CURRENT
// column that is blank on every other row. Trimming a line before splitting it
// on runs of 2+ spaces makes that blank cell disappear, shifting every value one
// column left — a non-current context's NAME then reads as its CLUSTER, and
// since both rows end up claiming the same NAME, Add's dedupe drops one of them
// outright.
func TestParseTableKeepsABlankFirstColumn(t *testing.T) {
	headers, rows, nameIdx := ParseTable(twoContextsOutput)

	if len(headers) != 5 || nameIdx != 1 {
		t.Fatalf("headers = %v, nameIdx = %d; want 5 headers with NAME at 1", headers, nameIdx)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0][0] != "" || rows[0][1] != "alt" {
		t.Errorf("non-current row = %q, want an empty CURRENT and NAME 'alt'", rows[0])
	}
	if rows[1][0] != "*" || rows[1][1] != "docker-desktop" {
		t.Errorf("current row = %q, want CURRENT '*' and NAME 'docker-desktop'", rows[1])
	}
}

// The consequence users actually hit: a context that isn't the active one has
// no index, so `kx context <n>` cannot reach it and the number it does offer
// points at a different context than the row shows.
func TestAddIndexesEveryContextIncludingNonCurrent(t *testing.T) {
	entriesTable := Service{}.Add(twoContextsOutput)
	_, entries := entriesTable.Text(), entriesTable.Entries

	if len(entries) != 2 {
		t.Fatalf("names = %v, want both contexts indexed", entries)
	}
	if entries[0].Name != "alt" || entries[1].Name != "docker-desktop" {
		t.Errorf("names = %v, want [alt docker-desktop]", entries)
	}
}

// The streaming path parses the header once and each row separately, so it needs
// the same treatment as ParseTable or `--watch` disagrees with a plain listing.
func TestTableShapeRowKeepsABlankFirstColumn(t *testing.T) {
	shape, ok := ParseHeader(strings.Split(twoContextsOutput, "\n")[0])
	if !ok {
		t.Fatal("ParseHeader returned ok=false")
	}

	row := shape.Row(strings.Split(twoContextsOutput, "\n")[1])

	if len(row) != 5 {
		t.Fatalf("row = %q, want 5 fields", row)
	}
	if row[0] != "" || row[1] != "alt" {
		t.Errorf("row = %q, want an empty CURRENT and NAME 'alt'", row)
	}
}

// The split cap has to shrink along with the row when a blank first column is
// inferred, or the last column stops being the unsplit remainder: a value
// carrying its own 2+-space run gets cut in two and the row grows a field it
// should not have. Same guarantee ParseTable already makes for an ordinary row
// (TestParseOutputLastColumnNotTruncated), held for the shifted shape too.
func TestBlankFirstColumnKeepsTheLastColumnWhole(t *testing.T) {
	output := "CURRENT   NAME   CLUSTER   NOTE\n" +
		"          alt    local     restarted  twice"

	_, rows, _ := ParseTable(output)

	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if len(rows[0]) != 4 {
		t.Fatalf("row = %q, want 4 fields", rows[0])
	}
	if rows[0][3] != "restarted  twice" {
		t.Errorf("last column = %q, want %q kept whole", rows[0][3], "restarted  twice")
	}
}

// Captured from `kubectl get pods -A`: NAMESPACE leads, and the same workload
// name recurs across namespaces — which is the whole reason -A went unindexed
// until now.
const allNamespacesOutput = "NAMESPACE      NAME                            READY   STATUS    RESTARTS   AGE\n" +
	"default        api-7d8f                        1/1     Running   0          5d\n" +
	"staging        api-7d8f                        1/1     Running   0          3d\n" +
	"staging        web-1                           1/1     Running   0          3d"

// An -A listing carries each row's namespace, so an index can resolve to the
// one place its resource actually lives.
func TestAddCarriesTheNamespaceOfEachRow(t *testing.T) {
	entriesTable := Service{}.Add(allNamespacesOutput)
	_, entries := entriesTable.Text(), entriesTable.Entries

	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want 3", entries)
	}
	if entries[0].Name != "api-7d8f" || entries[0].Namespace != "default" {
		t.Errorf("entries[0] = %+v, want api-7d8f in default", entries[0])
	}
	if entries[1].Name != "api-7d8f" || entries[1].Namespace != "staging" {
		t.Errorf("entries[1] = %+v, want api-7d8f in staging", entries[1])
	}
}

// The dedupe that keeps displayed indexes in step with saved state keys on NAME
// alone, which is right for a single namespace and wrong the moment a listing
// spans them: two namespaces running the same workload name are two resources,
// and collapsing them drops one from the table entirely.
func TestAddKeepsSameNamedRowsFromDifferentNamespaces(t *testing.T) {
	entriesTable := Service{}.Add(allNamespacesOutput)
	table, entries := entriesTable.Text(), entriesTable.Entries

	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want all three rows indexed", entries)
	}
	if strings.Count(table, "api-7d8f") != 2 {
		t.Errorf("table dropped a same-named row:\n%s", table)
	}
}

// Without a NAMESPACE column there is nothing to disambiguate by, so the
// name-only collapse has to stay — it is what stops a displayed index from
// pointing at a different resource than the saved one.
func TestAddStillCollapsesDuplicateNamesWithoutANamespaceColumn(t *testing.T) {
	duplicated := "NAME    READY   STATUS\n" +
		"api     1/1     Running\n" +
		"api     1/1     Running"

	entriesTable := Service{}.Add(duplicated)
	_, entries := entriesTable.Text(), entriesTable.Entries

	if len(entries) != 1 {
		t.Errorf("entries = %+v, want the duplicate name collapsed", entries)
	}
}

// Add already has the rows parsed; handing back only text makes the renderer
// parse them a second time, and a padded table cannot represent an empty cell —
// its gap is indistinguishable from column padding. Returning the rows is what
// lets a blank survive the journey from kubectl to the screen.
func TestAddReturnsTheRowsItParsed(t *testing.T) {
	table := Service{}.Add(twoContextsOutput)

	if !table.Indexable() {
		t.Fatal("Add reported a table it could not index")
	}
	want := []string{"X", "CURRENT", "NAME", "CLUSTER", "AUTHINFO", "NAMESPACE"}
	if len(table.Headers) != len(want) {
		t.Fatalf("Headers = %q, want %q", table.Headers, want)
	}
	for i := range want {
		if table.Headers[i] != want[i] {
			t.Errorf("Headers[%d] = %q, want %q", i, table.Headers[i], want[i])
		}
	}
	if len(table.Rows) != 2 {
		t.Fatalf("Rows = %q, want 2", table.Rows)
	}
	// The non-current row: index, then a blank CURRENT, then its name. Text
	// could not carry that middle cell; rows can.
	if got := table.Rows[0]; got[0] != "1" || got[1] != "" || got[2] != "alt" {
		t.Errorf("Rows[0] = %q, want index 1, an empty CURRENT and NAME alt", got)
	}
	if got := table.Rows[1]; got[1] != "*" || got[2] != "docker-desktop" {
		t.Errorf("Rows[1] = %q, want CURRENT '*' and NAME docker-desktop", got)
	}
}

// Output kx cannot index — JSON, YAML, a table with no NAME column — is carried
// through untouched for the caller to print as it came.
func TestAddCarriesNonTabularOutputThrough(t *testing.T) {
	raw := `{"kind":"PodList","items":[]}`

	table := Service{}.Add(raw)

	if table.Indexable() {
		t.Errorf("Add indexed non-tabular output: %+v", table)
	}
	if table.Text() != raw {
		t.Errorf("Text() = %q, want the input unchanged", table.Text())
	}
	if len(table.Entries) != 0 {
		t.Errorf("Entries = %+v, want none", table.Entries)
	}
}

// Text still renders the padded table, for the callers that genuinely need a
// string — and it round-trips through the parser, which is the property the
// old contract rested on entirely.
func TestTableTextRoundTrips(t *testing.T) {
	table := Service{}.Add(podsOutput)

	headers, rows, _ := ParseTable(table.Text())

	if len(headers) != len(table.Headers) || len(rows) != len(table.Rows) {
		t.Errorf("Text() re-parsed to %d headers/%d rows, want %d/%d",
			len(headers), len(rows), len(table.Headers), len(table.Rows))
	}
}
