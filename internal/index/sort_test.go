package index

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const podTable = `NAME   READY   STATUS             RESTARTS        AGE
web-b  1/1     Running            0               5d
api    0/1     CrashLoopBackOff   12 (3m ago)     3h
web-a  1/1     Running            2 (1h ago)      45s
job-x  0/1     Completed          0               <unknown>
cache  1/2     Running            5 (10m ago)     2y30d
`

// sortedNames sorts podTable by spec and returns the names in their new order.
func sortedNames(t *testing.T, spec string) []string {
	t.Helper()
	keys, err := ParseSortKeys(spec)
	if err != nil {
		t.Fatalf("ParseSortKeys(%q): %v", spec, err)
	}
	listing, ok := ParseListing(podTable)
	if !ok {
		t.Fatal("podTable did not parse")
	}
	sorted, err := listing.Sort(keys)
	if err != nil {
		t.Fatalf("Sort(%q): %v", spec, err)
	}
	return sorted.Names()
}

func wantOrder(t *testing.T, spec string, want ...string) {
	t.Helper()
	if got := sortedNames(t, spec); !slices.Equal(got, want) {
		t.Errorf("--sort %s = %v, want %v", spec, got, want)
	}
}

// Each column sorts as what it holds. Compared as text, AGE put 2y30d before
// 3h and 45s after 5d, RESTARTS put "12 (3m ago)" before "2 (1h ago)", and
// READY could not tell 1/2 from 1/1.
func TestSortReadsEachColumnAsItsValue(t *testing.T) {
	wantOrder(t, "age", "web-a", "api", "web-b", "cache", "job-x")
	wantOrder(t, "restarts", "web-b", "job-x", "web-a", "cache", "api")
	wantOrder(t, "ready", "api", "job-x", "cache", "web-b", "web-a")
	wantOrder(t, "name", "api", "cache", "job-x", "web-a", "web-b")
}

// A leading "-" sorts a key from largest down, and a placeholder sorts last
// either way: <unknown> is no age, not the youngest or the oldest.
func TestSortDescendingKeepsMissingLast(t *testing.T) {
	wantOrder(t, "-age", "cache", "web-b", "api", "web-a", "job-x")
}

// Keys apply in turn, and rows every key ties on keep kubectl's order. A
// stable sort is what kubectl's own --sort-by is not: sorting by name and
// then by status there scrambles the names inside each status.
func TestSortAppliesKeysInTurnAndIsStable(t *testing.T) {
	wantOrder(t, "status,-restarts", "job-x", "api", "cache", "web-a", "web-b")
	wantOrder(t, "status", "job-x", "api", "web-b", "web-a", "cache")
}

// A key is typed the way the header reads, whatever its case or kubectl's
// unit note: cpu is CPU(cores), and nominated-node is NOMINATED NODE.
func TestSortMatchesColumnsAsTyped(t *testing.T) {
	headers := []string{"NAME", "CPU(cores)", "MEMORY(bytes)", "CPU%", "NOMINATED NODE"}
	rows := [][]string{
		{"a", "1500m", "1Gi", "90%", "n2"},
		{"b", "2", "128Mi", "9%", "n1"},
		{"c", "250m", "512Mi", "10%", "<none>"},
	}
	for spec, want := range map[string][]string{
		"cpu":            {"c", "a", "b"},
		"-MEMORY":        {"a", "c", "b"},
		"cpu%":           {"b", "c", "a"},
		"nominated-node": {"b", "a", "c"},
		"Nominated_Node": {"b", "a", "c"},
	} {
		keys, _ := ParseSortKeys(spec)
		sorted, err := SortRows(headers, rows, keys)
		if err != nil {
			t.Fatalf("SortRows(%q): %v", spec, err)
		}
		var got []string
		for _, row := range sorted {
			got = append(got, row[0])
		}
		if !slices.Equal(got, want) {
			t.Errorf("--sort %s = %v, want %v", spec, got, want)
		}
	}
}

// A key no column answers to is refused with the columns there are, typed as
// a key would be, rather than quietly leaving the order alone.
func TestSortRefusesAnUnknownColumn(t *testing.T) {
	listing, _ := ParseListing(podTable)
	_, err := listing.Sort([]SortKey{{Column: "restart"}})
	var unknown UnknownColumnError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want an UnknownColumnError", err)
	}
	if got := strings.Join(unknown.Accepted, ", "); got != "name, ready, status, restarts, age" {
		t.Errorf("Accepted = %q, want the table's columns", got)
	}
}

// An empty key — a doubled or trailing comma, a lone "-" — names nothing.
func TestParseSortKeysRefusesAnEmptyKey(t *testing.T) {
	for _, spec := range []string{"", "status,", "status,,age", "-"} {
		if _, err := ParseSortKeys(spec); err == nil {
			t.Errorf("ParseSortKeys(%q) succeeded, want it refused", spec)
		}
	}
	keys, err := ParseSortKeys(" status , -age ")
	if err != nil || len(keys) != 2 || keys[0] != (SortKey{Column: "status"}) || keys[1] != (SortKey{Column: "age", Descending: true}) {
		t.Errorf("ParseSortKeys = %v, %v; want status then descending age", keys, err)
	}
}

// One stray cell turns a column to text rather than sorting numbers among
// words: a column is read as one kind of value throughout.
func TestSortReadsAMixedColumnAsText(t *testing.T) {
	headers := []string{"NAME", "VALUE"}
	rows := [][]string{{"a", "10"}, {"b", "9"}, {"c", "n/a"}}
	sorted, _ := SortRows(headers, rows, []SortKey{{Column: "value"}})
	if got := sorted[0][0] + sorted[1][0] + sorted[2][0]; got != "abc" {
		t.Errorf("order = %s, want text order abc (\"10\" < \"9\" < \"n/a\")", got)
	}
}

// Ties keep their order however many rows there are. Small inputs are sorted
// by insertion, which is stable by accident, so this takes enough rows that
// an unstable sort would scramble them, as kubectl's --sort-by does a status.
func TestSortKeepsTiesInOrderAtSize(t *testing.T) {
	headers := []string{"NAME", "STATUS"}
	var rows [][]string
	for i := 0; i < 40; i++ {
		status := "Running"
		if i%3 == 0 {
			status = "Pending"
		}
		rows = append(rows, []string{string(rune('a'+i/26)) + string(rune('a'+i%26)), status})
	}
	sorted, _ := SortRows(headers, rows, []SortKey{{Column: "status"}})
	for i := 1; i < len(sorted); i++ {
		if sorted[i][1] == sorted[i-1][1] && sorted[i][0] < sorted[i-1][0] {
			t.Fatalf("row %d (%s) came after %s within %s: ties were reordered", i, sorted[i][0], sorted[i-1][0], sorted[i][1])
		}
	}
}

// kx top writes "—" where a Pod has no limit to measure against. It is no
// value, and sorts last; read as text it made the column text, and -cpu% put
// every "—" first and 9% above 50%.
func TestSortReadsKxsDashAsMissing(t *testing.T) {
	headers := []string{"NAME", "CPU%"}
	rows := [][]string{{"a", "—"}, {"b", "9%"}, {"c", "50%"}, {"d", "—"}}
	sorted, _ := SortRows(headers, rows, []SortKey{{Column: "cpu%", Descending: true}})
	var got string
	for _, row := range sorted {
		got += row[0]
	}
	if got != "cbad" {
		t.Errorf("order = %s, want cbad: 50%%, 9%%, then the rows with no limit", got)
	}
}
