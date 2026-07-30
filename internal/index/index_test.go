package index

import (
	"github.com/mattn/go-runewidth"
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

func TestAddPrependsIndexColumn(t *testing.T) {
	output, names := Service{}.Add(podsOutput)
	lines := strings.Split(output, "\n")
	if !strings.HasPrefix(lines[0], "X") {
		t.Errorf("header = %q, want it to start with X", lines[0])
	}
	if !strings.HasPrefix(lines[1], "1") || !strings.HasPrefix(lines[2], "2") {
		t.Errorf("indexes are not 1-based: %q, %q", lines[1], lines[2])
	}
	want := []string{"nginx-abc-xyz", "redis-def-uvw"}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestAddSingleRow(t *testing.T) {
	_, names := Service{}.Add(singleRowOutput)
	if len(names) != 1 || names[0] != "only-pod-abc" {
		t.Errorf("names = %v, want [only-pod-abc]", names)
	}
}

func TestAddEmptyOutputReturnsOriginal(t *testing.T) {
	output, names := Service{}.Add("")
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
		output, names := Service{}.Add(raw)
		if output != raw || names != nil {
			t.Errorf("Add(%q) = %q, %v; want unchanged", raw, output, names)
		}
	}
}

func TestAddLastColumnNotTruncated(t *testing.T) {
	output, _ := Service{}.Add(contextsOutput)
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
	output, names := Service{}.Add(duplicated)
	if len(names) != 2 || names[0] != "pod-a" || names[1] != "pod-b" {
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

// Padding is measured in terminal columns, not bytes, matching the tables
// internal/render draws itself.
//
// "日本語" is 9 bytes but occupies 6 columns. Padding by byte length pads it to
// 9 and every other cell to 9 bytes as well, so the rows come out visibly
// different widths on a terminal. Every cell is padded, so the invariant is
// simply that all rows render to the same display width.
func TestFormatPadsByDisplayWidth(t *testing.T) {
	out := Format([][]string{
		{"NAME", "STATUS"},
		{"日本語", "Running"},
		{"web", "Running"},
	})
	seen := map[int][]string{}
	for _, line := range strings.Split(out, "\n") {
		w := runewidth.StringWidth(line)
		seen[w] = append(seen[w], line)
	}
	if len(seen) != 1 {
		t.Errorf("rows render at %d different display widths, want 1:\n%s\n%v",
			len(seen), out, seen)
	}
}

// ASCII is all kubectl emits for built-in resources, so the layout every other
// test pins must be untouched by measuring in columns.
func TestFormatIsUnchangedForASCII(t *testing.T) {
	rows := [][]string{{"X", "NAME", "AGE"}, {"1", "web", "5d"}, {"2", "redis-longer", "3d"}}
	want := "X  NAME          AGE\n1  web           5d \n2  redis-longer  3d "
	if got := Format(rows); got != want {
		t.Errorf("Format =\n%q\nwant\n%q", got, want)
	}
}
