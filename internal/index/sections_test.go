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
