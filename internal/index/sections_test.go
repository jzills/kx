package index

import (
	"fmt"
	"strings"
	"testing"
)

// What `kubectl get deploy,svc` prints: a table per kind, each under its own
// header, with a blank line between them and every name carrying its kind.
const multiKindOutput = "NAME                         READY   UP-TO-DATE   AVAILABLE   AGE\n" +
	"deployment.apps/api          1/1     1            1           5d\n" +
	"deployment.apps/web          2/2     2            2           3d\n" +
	"\n" +
	"NAME                 TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
	"service/api          ClusterIP   10.0.0.11    <none>        80/TCP    5d\n"

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

// Only the first table's header was read as one. The second became a row —
// numbered, saved, and resolving to a resource called "NAME" — and every
// later header was deduped away as a second "NAME", so each table after the
// first sat under the first one's columns: a Service's TYPE under READY.
func TestAddNumbersEveryTableOfAMultiKindListing(t *testing.T) {
	table := Service{}.Add(multiKindOutput)

	if got, want := entryNames(table.Entries),
		[]string{"deployment.apps/api", "deployment.apps/web", "service/api"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("entries = %q, want %q — no header among them", got, want)
	}
	if len(table.Sections) != 2 {
		t.Fatalf("got %d sections, want one per table kubectl printed", len(table.Sections))
	}
	deployments, services := table.Sections[0], table.Sections[1]
	if got := strings.Join(deployments.Headers, " "); got != "X NAME READY UP-TO-DATE AVAILABLE AGE" {
		t.Errorf("first section headers = %q", got)
	}
	if got := strings.Join(services.Headers, " "); got != "X NAME TYPE CLUSTER-IP EXTERNAL-IP PORT(S) AGE" {
		t.Errorf("second section headers = %q, want the Service table's own", got)
	}
	if len(services.Rows) != 1 || services.Rows[0][0] != "3" || services.Rows[0][2] != "ClusterIP" {
		t.Errorf("second section rows = %q, want index 3 with TYPE in its own column", services.Rows)
	}
	if got := len(table.Rows); got != 3 {
		t.Errorf("Rows holds %d, want every section's rows", got)
	}
}

// A listing of one kind is one table, as before: no sections to draw apart.
func TestAddLeavesASingleTableUnsectioned(t *testing.T) {
	table := Service{}.Add("NAME   READY\nnginx  1/1\n")
	if table.Sections != nil {
		t.Errorf("Sections = %v for a single table, want nil", table.Sections)
	}
	if len(table.Entries) != 1 {
		t.Errorf("entries = %v", table.Entries)
	}
}

// Each table of an -A listing has its own NAMESPACE column, and each row is
// placed from it.
func TestAddPlacesEveryTableOfASpanningListing(t *testing.T) {
	output := "NAMESPACE   NAME             READY   STATUS    RESTARTS   AGE\n" +
		"prod        pod/api-1        1/1     Running   0          5d\n" +
		"\n" +
		"NAMESPACE   NAME             TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
		"staging     service/api      ClusterIP   10.0.0.11    <none>        80/TCP    5d\n"
	table := Service{}.Add(output)
	want := []Entry{{Name: "pod/api-1", Namespace: "prod"}, {Name: "service/api", Namespace: "staging"}}
	if fmt.Sprint(table.Entries) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", table.Entries, want)
	}
}

// --match narrows each table, drops a table it empties rather than printing
// its header over nothing, and matches the name, not the kind in front of it:
// "app" is in every "deployment.apps/…".
func TestAddMatchingNarrowsEachTableByName(t *testing.T) {
	table := Service{}.AddMatching(multiKindOutput, "web")
	if got := entryNames(table.Entries); fmt.Sprint(got) != fmt.Sprint([]string{"deployment.apps/web"}) {
		t.Errorf("entries = %q, want deployment.apps/web alone", got)
	}
	if len(table.Sections) > 1 {
		t.Errorf("kept %d sections, want the emptied Service table dropped", len(table.Sections))
	}
	if table.Match != "web" {
		t.Errorf("Match = %q, want the term", table.Match)
	}

	if table := (Service{}).AddMatching(multiKindOutput, "app"); len(table.Entries) != 0 {
		t.Errorf("'app' matched %q through the apps group in the kind prefix", entryNames(table.Entries))
	}
}

// ParseTables hands back each of kubectl's tables apart, so a caller that
// stitches several replies — one per namespace — can keep a kind's rows under
// that kind's own columns.
func TestParseTablesKeepsEachTableApart(t *testing.T) {
	tables, ok := ParseTables(multiKindOutput)
	if !ok || len(tables) != 2 {
		t.Fatalf("ParseTables = %d tables (ok %v), want 2", len(tables), ok)
	}
	if got := strings.Join(tables[1].Headers, " "); got != "NAME TYPE CLUSTER-IP EXTERNAL-IP PORT(S) AGE" {
		t.Errorf("second table's headers = %q, want the Service table's", got)
	}
	if len(tables[0].Rows) != 2 || len(tables[1].Rows) != 1 || tables[1].Rows[0][1] != "ClusterIP" {
		t.Errorf("rows = %q / %q, want two Deployments and one Service under its own columns",
			tables[0].Rows, tables[1].Rows)
	}
	if _, ok := ParseTables(`{"items":[]}`); ok {
		t.Error("JSON parsed as a table")
	}
}

// AddTables numbers several tables as one listing, the indexes running on
// from one into the next, and drops a table with no rows left rather than
// drawing a header over nothing.
func TestAddTablesNumbersOnAndDropsAnEmptyTable(t *testing.T) {
	table := Service{}.AddTables([]RawTable{
		{Headers: []string{"NAME", "READY"}, Rows: [][]string{{"deployment.apps/api", "1/1"}}},
		{Headers: []string{"NAME", "DATA"}},
		{Headers: []string{"NAME", "TYPE"}, Rows: [][]string{{"service/api", "ClusterIP"}}},
	})
	if len(table.Sections) != 2 {
		t.Fatalf("got %d sections, want the empty table dropped", len(table.Sections))
	}
	if got := table.Sections[1].Rows[0][0]; got != "2" {
		t.Errorf("the second table starts at index %s, want 2", got)
	}
	if got, want := entryNames(table.Entries), []string{"deployment.apps/api", "service/api"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %q, want %q", got, want)
	}

	one := Service{}.AddTables([]RawTable{{Headers: []string{"NAME", "READY"}, Rows: [][]string{{"api", "1/1"}}}})
	if one.Sections != nil || len(one.Rows) != 1 {
		t.Errorf("one table = %+v, want an ordinary listing", one)
	}

	none := Service{}.AddTables([]RawTable{{Headers: []string{"NAME", "READY"}}, {Headers: []string{"NAME", "TYPE"}}})
	if !none.Indexable() || !none.Empty() {
		t.Errorf("all tables empty = %+v, want an empty listing under the first header", none)
	}
}

// A listing kx prints without numbering keeps every row the term matches,
// however alike two of them read. Numbering collapses a row that repeats an
// earlier one, so that indexes stay one-to-one with saved state, and narrowing
// that ran through it printed one ServiceAccount where kubectl listed eight:
// kx get sa -A -o custom-columns=NAME:.metadata.name,UID:.metadata.uid -m
// default has no NAMESPACE column to hold them apart.
func TestListingNarrowKeepsRowsNumberingWouldCollapse(t *testing.T) {
	listing, ok := ParseListing("NAME      UID\n" +
		"default   uid-1\n" +
		"builder   uid-2\n" +
		"default   uid-3\n")
	if !ok {
		t.Fatal("ParseListing refused a table")
	}

	text := listing.Narrow("default").Unnumbered()
	for _, uid := range []string{"uid-1", "uid-3"} {
		if !strings.Contains(text, uid) {
			t.Errorf("narrowed listing dropped the row holding %s:\n%s", uid, text)
		}
	}
	if strings.Contains(text, "builder") {
		t.Errorf("narrowed listing kept a row the term does not match:\n%s", text)
	}
	if strings.Contains(text, "X ") || strings.HasPrefix(text, "X") {
		t.Errorf("unnumbered listing carries an index column:\n%s", text)
	}
}

// Unnumbered lays each of kubectl's tables out under its own header, and
// drops a table the term emptied, as a numbered listing does.
func TestListingUnnumberedKeepsEachTableAndDropsAnEmptyOne(t *testing.T) {
	listing, ok := ParseListing(multiKindOutput)
	if !ok {
		t.Fatal("ParseListing refused a listing of several kinds")
	}

	all := listing.Unnumbered()
	if tables := strings.Split(all, "\n\n"); len(tables) != 2 ||
		!strings.HasPrefix(tables[1], "NAME") || !strings.Contains(tables[1], "ClusterIP") {
		t.Errorf("unnumbered = %q, want two tables, the Service under its own header", all)
	}

	narrowed := listing.Narrow("web")
	if text := narrowed.Unnumbered(); strings.Contains(text, "\n\n") || strings.Contains(text, "TYPE") {
		t.Errorf("narrowed to web = %q, want the emptied Service table dropped", text)
	}
	if narrowed.Empty() {
		t.Error("Empty() with deployment.apps/web left")
	}
	if !listing.Narrow("nothing-is-called-this").Empty() {
		t.Error("Empty() = false after the term matched no row")
	}
}

// ListingOf takes tables already parsed — replies stitched together — and
// narrows and lays them out as ParseListing's do.
func TestListingOfStitchedTables(t *testing.T) {
	listing, ok := ListingOf([]RawTable{
		{Headers: []string{"NAMESPACE", "NAME"}, Rows: [][]string{{"prod", "web"}, {"prod", "web"}}},
	})
	if !ok {
		t.Fatal("ListingOf refused tables with a NAME column")
	}
	if text := listing.Unnumbered(); strings.Count(text, "web") != 2 {
		t.Errorf("unnumbered = %q, want both rows named web", text)
	}
	if _, ok := ListingOf([]RawTable{{Headers: []string{"POD-ONLY"}}}); ok {
		t.Error("ListingOf accepted a table with no NAME column")
	}
	if _, ok := ListingOf(nil); ok {
		t.Error("ListingOf accepted no tables")
	}
}
