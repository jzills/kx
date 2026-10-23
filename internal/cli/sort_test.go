package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/state"
)

const sortPods = "NAME    READY   STATUS             RESTARTS       AGE\n" +
	"web-b   1/1     Running            0              5d\n" +
	"api     0/1     CrashLoopBackOff   12 (3m ago)    3h\n" +
	"web-a   1/1     Running            2 (1h ago)     45s\n"

// names resolves every index of the current listing, in order.
func names(t *testing.T, store *state.Service) []string {
	t.Helper()
	entry, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var got []string
	for i := 1; i <= entry.Resources.Len(); i++ {
		name, _, _, err := store.Fields(i)
		if err != nil {
			t.Fatalf("Fields(%d): %v", i, err)
		}
		got = append(got, name)
	}
	return got
}

// Indexes follow the sorted order: row 1 is the first sorted row, so the
// number on screen is the number that resolves. kubectl is asked without the
// flag, which is kx's, and the saved query keeps it, so a refresh lists the
// same order again.
func TestGetSortNumbersTheSortedRows(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	if err := executeArgv(newGetCommand(services), []string{"pods", "--sort", "-restarts"}); err != nil {
		t.Fatalf("kx get pods --sort -restarts: %v", err)
	}
	if got, want := names(t, services.State), []string{"api", "web-a", "web-b"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if got := joinArgs(kube.args); strings.Contains(got, "--sort") {
		t.Errorf("kubectl = %q, want no --sort", got)
	}
	entry, _ := services.State.Load()
	if !slices.Contains(entry.Query.Args, "--sort=-restarts") {
		t.Errorf("saved query args = %q, want --sort=-restarts recorded", entry.Query.Args)
	}

	// The control: unsorted, the same reply numbers in kubectl's order.
	if err := executeArgv(newGetCommand(services), []string{"pods"}); err != nil {
		t.Fatalf("kx get pods: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-b", "api", "web-a"}; !slices.Equal(got, want) {
		t.Errorf("unsorted indexes resolve to %v, want kubectl's order %v", got, want)
	}
}

// A saved listing that records a sort is listed again in that order: the
// args a refresh replays are the ones Execute recorded.
func TestGetSortIsReplayedFromTheSavedQuery(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	get := GetCommand{Kubectl: kube, State: services.State, Index: services.Index}
	if _, _, err := get.Execute("pods", "", []string{"--sort=age"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-a", "api", "web-b"}; !slices.Equal(got, want) {
		t.Errorf("replayed listing resolves to %v, want %v", got, want)
	}
}

// kubectl's --sort-by is forwarded and applies first; kx's --sort sorts the
// reply after it, stably, so the JSONPath decides the order of ties.
func TestGetSortBesideSortBy(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	err := executeArgv(newGetCommand(services), []string{"pods", "--sort-by=.metadata.name", "--sort", "ready"})
	if err != nil {
		t.Fatalf("kx get pods --sort-by --sort: %v", err)
	}
	if got := joinArgs(kube.args); !strings.Contains(got, "--sort-by=.metadata.name") || strings.Contains(got, "--sort=") {
		t.Errorf("kubectl = %q, want --sort-by forwarded and --sort kept", got)
	}
	// READY ties web-b and web-a at 1/1; they keep the reply's order.
	if got, want := names(t, services.State), []string{"api", "web-b", "web-a"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}

// Each refusal is made before kubectl is asked, except a column the reply
// turns out not to have, which only the reply can say.
func TestGetSortRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pods", "--sort", "status,"},
			"Invalid value for '--sort': 'status,' names no column — name each column, separated by commas: 'status,-restarts'."},
		{[]string{"pods", "--sort="},
			"Invalid value for '--sort': '' names no column — name each column, separated by commas: 'status,-restarts'."},
		{[]string{"pods", "--sort", "age", "--watch"},
			"'--sort' cannot be combined with '--watch' — a watch streams rows as they change, so there is no finished table to sort."},
		{[]string{"pods", "--sort", "age", "-o", "json"},
			"'--sort' cannot be combined with '-o json' — it sorts the table kx draws, and that output is for a program; use '--sort-by' for kubectl's own order."},
		{[]string{"pods", "--sort", "age", "--no-headers"},
			"'--sort' cannot be combined with '--no-headers' — it sorts the table kx draws, and that output is for a program; use '--sort-by' for kubectl's own order."},
		{[]string{"secrets", "--sort", "age", "--decode"},
			"'--sort' cannot be combined with '--decode' — it sorts a listing, and '--decode' prints a Secret's data."},
		{[]string{"contexts", "--sort", "name"},
			"kx get contexts does not support '--sort' — only a resource listing can be sorted."},
	} {
		kube := &fakeKubectl{output: sortPods, namespace: "prod"}
		err := executeArgv(newGetCommand(switchServices(t, kube)), tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx get %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
		if len(kube.calls) > 0 {
			t.Errorf("kx get %s: kubectl was called: %q", strings.Join(tc.args, " "), kube.calls)
		}
	}

	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	err := executeArgv(newGetCommand(switchServices(t, kube)), []string{"pods", "--sort", "restart"})
	want := "Invalid value for '--sort': 'restart'. Accepted values: name, ready, status, restarts, age."
	if err == nil || err.Error() != want {
		t.Errorf("kx get pods --sort restart: err = %v, want %q", err, want)
	}
}

// A table with no NAME column is one kx cannot read, and was printed in
// kubectl's order as though sorted, here and from another cluster alike.
// The control is the same columns with a NAME among them, which do sort.
func TestGetSortRefusesATableWithNoNameColumn(t *testing.T) {
	const columns = "custom-columns=POD:.metadata.name,NODE:.spec.nodeName"
	const reply = "POD     NODE\nweb-b   node-2\napi     node-1\n"
	want := "'--sort' reads a table by its NAME column, and kubectl's reply has none — " +
		"add one to the custom columns (NAME:.metadata.name), or use '--sort-by' for kubectl's own order."
	for _, args := range [][]string{
		{"pods", "-o", columns, "--sort", "node"},
		{"pods", "-o", columns, "--sort", "node", "--context=b"},
	} {
		kube := &fakeKubectl{output: reply, namespace: "prod"}
		err := executeArgv(newGetCommand(switchServices(t, kube)), args)
		if err == nil || err.Error() != want {
			t.Errorf("kx get %s: err = %v, want %q", strings.Join(args, " "), err, want)
		}
	}
	// A kind's own table without one, as kubectl's events table is.
	kube := &fakeKubectl{output: "LAST SEEN   TYPE      REASON\n3m          Warning   BackOff\n", namespace: "prod"}
	err := executeArgv(newGetCommand(switchServices(t, kube)), []string{"events", "--sort", "type"})
	if want := "'--sort' reads a table by its NAME column, and kubectl's table for this resource has none. " +
		"Use '--sort-by' for kubectl's own order."; err == nil || err.Error() != want {
		t.Errorf("kx get events --sort type: err = %v, want %q", err, want)
	}

	kube = &fakeKubectl{output: "NAME    NODE\nweb-b   node-2\napi     node-1\n", namespace: "prod"}
	services := switchServices(t, kube)
	args := []string{"pods", "-o", "custom-columns=NAME:.metadata.name,NODE:.spec.nodeName", "--sort", "node"}
	if err := executeArgv(newGetCommand(services), args); err != nil {
		t.Fatalf("kx get %s: %v", strings.Join(args, " "), err)
	}
	if got, want := names(t, services.State), []string{"api", "web-b"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}

// kx top sorts after its own percentage columns are added, so they sort —
// which kubectl top's --sort-by, cpu or memory only, cannot do — and a node
// listing sorts by its relabeled CPU%.
func TestTopSortNumbersTheSortedRows(t *testing.T) {
	quietRender(t)
	pods := &scriptedKubectl{outputs: []string{topPodsFixture, podsJSON}}
	services := topServices(t, pods)
	if err := executeArgv(newTopCommand(services), []string{"--sort", "-memory"}); err != nil {
		t.Fatalf("kx top --sort -memory: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-2", "web-1"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if got := joinArgs(pods.calls[0]); strings.Contains(got, "--sort") {
		t.Errorf("kubectl = %q, want no --sort", got)
	}

	quietRender(t)
	nodes := &scriptedKubectl{outputs: []string{nodesOutput}}
	services = topServices(t, nodes)
	if err := executeArgv(newTopCommand(services), []string{"nodes", "--sort", "-cpu%"}); err != nil {
		t.Fatalf("kx top nodes --sort -cpu%%: %v", err)
	}
	if got, want := names(t, services.State), []string{"node-b", "node-a"}; !slices.Equal(got, want) {
		t.Errorf("node indexes resolve to %v, want %v", got, want)
	}
	entry, _ := services.State.Load()
	if !slices.Contains(entry.Query.Args, "--sort=-cpu%") {
		t.Errorf("saved query args = %q, want --sort=-cpu%% recorded", entry.Query.Args)
	}
}

// kx scan's rows come back in the order images were resolved, not by
// severity, so --sort -crit,-high is how a sweep answers which image is
// worst. A failed scan has no counts and sorts last, as its dashes say.
func TestScanSortOrdersTheSummary(t *testing.T) {
	counts := func(critical, high int) map[string]int {
		return map[string]int{"CRITICAL": critical, "HIGH": high}
	}
	rows := []scanner.ImageScan{
		{Image: "a", Counts: counts(0, 9)},
		{Image: "failed", Error: "pull failed"},
		{Image: "b", Counts: counts(3, 1)},
		{Image: "c", Counts: counts(3, 4)},
		// Clean, and after the failed scan: read as zeroes, the failed scan
		// would tie with it and stay ahead of it.
		{Image: "clean", Counts: counts(0, 0)},
	}
	keys, err := scanSortKeys("-crit,-high")
	if err != nil {
		t.Fatalf("scanSortKeys: %v", err)
	}
	var got []string
	for _, row := range sortScans(rows, keys) {
		got = append(got, row.Image)
	}
	if want := []string{"c", "b", "a", "clean", "failed"}; !slices.Equal(got, want) {
		t.Errorf("sorted images = %v, want %v", got, want)
	}
}

// The summary's columns are fixed, so a key it lacks — and a key naming
// nothing — is refused before kubectl or any scanner runs, as is --sort
// beside --full, whose output is the scanner's own. A --sort with no value
// at all is refused as every value flag's is: the check for an empty one
// once ran first and called it a key naming nothing.
func TestScanSortRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--sort", "crti"},
			"Invalid value for '--sort': 'crti'. Accepted values: image, crit, high, med, low, unspec."},
		{[]string{"--sort="},
			"Invalid value for '--sort': '' names no column — name each column, separated by commas: 'status,-restarts'."},
		{[]string{"--sort", ""},
			"Invalid value for '--sort': '' names no column — name each column, separated by commas: 'status,-restarts'."},
		{[]string{"--sort"}, "'--sort' needs a value."},
		{[]string{"--sort", "-crit", "--full"},
			"'--sort' cannot be combined with '--full' — '--full' streams the scanner's own report, which kx does not parse, so there is no table to sort."},
	} {
		kube := &fakeKubectl{namespace: "prod", output: `{"items":[]}`}
		fake := &fakeScanner{}
		cmd := newScanCommand(Services{
			Kubectl: kube, Config: config.Default(), Scanner: fake,
			State: &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		})
		// kx scan parses its own argv (DisableFlagParsing), so RunE takes it
		// whole, as the other scan tests drive it.
		err := cmd.RunE(cmd, tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx scan %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
		if len(kube.calls) > 0 || len(fake.argv) > 0 {
			t.Errorf("kx scan %s: ran kubectl %q or the scanner %q", strings.Join(tc.args, " "), kube.calls, fake.argv)
		}
	}
}

// A sweep is ranked most severe first, and --sort orders it on top: the keys
// decide, and the ranking orders what they tie on. VERDICT sorts by severity
// — as text, -verdict put warning above critical — and the indexes saved
// follow the sorted order, with the query recording the sort for a refresh.
func TestDiagSortOrdersTheRankedSweep(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		healthy(kinds.Deployment, "a-ok", "prod"),
		unhealthy(kinds.StatefulSet, "db", "prod"),
		unhealthy(kinds.Deployment, "z-bad", "prod"),
		healthy(kinds.StatefulSet, "cache", "prod"),
	}}
	var saved []state.State
	command := triageOf(gatherer, &saved)
	command.Sort = "kind"
	result, err := command.Execute(context.Background(), "prod", false, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var got []string
	for _, report := range result.All {
		got = append(got, report.Name)
	}
	// Deployments, then StatefulSets; the critical one first within each.
	if want := []string{"z-bad", "a-ok", "db", "cache"}; !slices.Equal(got, want) {
		t.Errorf("sorted sweep = %v, want %v", got, want)
	}
	last := saved[len(saved)-1]
	if first, ok := last.Resources.At(1); last.Resources.Len() != 4 || !ok || first.Name != "z-bad" {
		t.Errorf("saved listing = %v, want index 1 to be z-bad", last.Resources)
	}
	if !slices.Contains(last.Query.Args, "--sort=kind") {
		t.Errorf("saved query args = %q, want --sort=kind recorded", last.Query.Args)
	}

	reports := result.All
	ascending, _ := sortReports(reports, "verdict,name")
	if ascending[0].Name != "a-ok" || ascending[len(ascending)-1].Name != "z-bad" {
		t.Errorf("verdict,name = %v first and %v last, want healthy first and z-bad last",
			ascending[0].Name, ascending[len(ascending)-1].Name)
	}
}

// The default table prints the unhealthy rows of a sweep that saved every
// swept resource, so each row's number is its place in the saved listing and
// not its place in the table. Severity alone keeps the two the same — the
// unhealthy rows lead the sweep — but a --sort key deals healthy rows in
// between them, and a table numbering its own rows then offered index 1 for a
// resource saved second: kx diag 1, kx delete 1 and the rest acted on the
// healthy resource above it. The sorted test above runs with --show-healthy,
// where every swept resource is a row and the two numberings agree again.
func TestDiagSortNumbersRowsByTheSavedListing(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		healthy(kinds.Deployment, "a-ok", "prod"),
		unhealthy(kinds.StatefulSet, "db", "prod"),
		unhealthy(kinds.Deployment, "z-bad", "prod"),
		healthy(kinds.StatefulSet, "cache", "prod"),
	}}
	var saved []state.State
	command := triageOf(gatherer, &saved)
	command.Sort = "name"
	result, err := command.Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// a-ok, cache, db, z-bad by name; the two unhealthy ones are printed.
	if len(result.Reports) != 2 {
		t.Fatalf("printed %d rows, want the two unhealthy ones", len(result.Reports))
	}
	var got []int
	for _, row := range result.Reports {
		got = append(got, row.Index)
	}
	if want := []int{3, 4}; !slices.Equal(got, want) {
		t.Errorf("row indexes = %v, want %v — the rows' places in the sorted sweep", got, want)
	}
	listing := saved[len(saved)-1].Resources
	for _, row := range result.Reports {
		resource, ok := listing.At(row.Index)
		if !ok {
			t.Fatalf("index %d resolves to nothing in a listing of %d", row.Index, listing.Len())
		}
		if resource.Name != row.Name || resource.Kind != row.Kind {
			t.Errorf("index %d resolves to %s/%s, but the table prints it as %s/%s",
				row.Index, resource.Kind, resource.Name, row.Kind, row.Name)
		}
	}
}

// A sweep's columns are fixed, so its --sort is checked before anything is
// swept, and refused beside an index, whose report has no rows to order.
func TestDiagSortRefusals(t *testing.T) {
	want := "Invalid value for '--sort': 'severity'. Accepted values: kind, name, namespace, verdict, top-finding."
	if err := sweepSortError("severity"); err == nil || err.Error() != want {
		t.Errorf("sweepSortError(severity) = %v, want %q", err, want)
	}
	if err := sweepSortError("-verdict,top-finding"); err != nil {
		t.Errorf("sweepSortError(-verdict,top-finding) = %v, want nil", err)
	}
	// Beside an index, --sort is refused for the index whatever it names: a
	// column the sweep lacks was once the refusal, sending the user to fix a
	// key for a flag kx diag 1 cannot take.
	beside := "'--sort' cannot be combined with an index — it sorts a namespace sweep. Drop the flag, or drop the index to sweep the namespace instead."
	for _, args := range [][]string{{"1", "--sort", "kind"}, {"1", "--sort", "restarts"}} {
		services := switchServices(t, &fakeKubectl{namespace: "prod"})
		err := executeArgv(newDiagnosticCommand(services, "diagnostic", nil), args)
		if err == nil || err.Error() != beside {
			t.Errorf("kx diag %s: err = %v, want %q", strings.Join(args, " "), err, beside)
		}
	}
	// The control: without an index, the same key is refused for its column.
	services := switchServices(t, &fakeKubectl{namespace: "prod"})
	err := executeArgv(newDiagnosticCommand(services, "diagnostic", nil), []string{"--sort", "restarts"})
	want = "Invalid value for '--sort': 'restarts'. Accepted values: kind, name, namespace, verdict, top-finding."
	if err == nil || err.Error() != want {
		t.Errorf("kx diag --sort restarts: err = %v, want %q", err, want)
	}
}

// An empty namespace is a listing with no rows, not an error, and --sort has
// nothing to order there: kubectl puts "No resources found" on stderr, so the
// reply kx sorts has no columns to name the key against. The key was refused
// against those absent headers — "Accepted values: ." — and the refusal took
// the save with it, so the listing before it went on resolving indexes into a
// namespace the screen had left.
func TestSortAnEmptyListingOrdersNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"get", []string{"pods", "--sort", "name"}},
		{"top", []string{"--sort", "name"}},
		{"top nodes", []string{"nodes", "--sort", "name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &fakeKubectl{output: sortPods, namespace: "prod"}
			services := switchServices(t, kube)
			// Seeded with a listing that found something, under a query of
			// its own so the empty one pushes rather than replacing it.
			if err := executeArgv(newGetCommand(services), []string{"pods", "-m", "web"}); err != nil {
				t.Fatalf("seeding kx get pods -m web: %v", err)
			}
			if got := names(t, services.State); len(got) == 0 {
				t.Fatal("the seeded listing resolves nothing, so there is no stale index to retire")
			}

			kube.output = ""
			command := newGetCommand(services)
			if tc.name != "get" {
				command = newTopCommand(services)
			}
			if err := executeArgv(command, tc.argv); err != nil {
				t.Fatalf("kx %s: %v", tc.name, err)
			}
			entry, err := services.State.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if entry.Resources.Len() != 0 {
				t.Errorf("the empty listing resolves to %v, want the seeded rows retired", entry.Resources)
			}
		})
	}
}
