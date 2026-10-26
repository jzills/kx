package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// A limit keeps the first rows of the sorted order and numbers only them, so
// the saved listing holds what the screen shows and an index past it names
// nothing. The caption counts the rows kept against the rows there were. The
// control is the same listing without the flag: every row, and a plain count.
func TestGetLimitKeepsTheFirstSortedRows(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	out := captureRender(t)
	if err := executeArgv(newGetCommand(services), []string{"pods", "--sort", "-restarts", "--limit", "2"}); err != nil {
		t.Fatalf("kx get pods --sort -restarts --limit 2: %v", err)
	}
	if got, want := names(t, services.State), []string{"api", "web-a"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "Pods · prod · 2 of 3 items") {
		t.Errorf("caption missing 'Pods · prod · 2 of 3 items':\n%s", out)
	}
	if got := joinArgs(kube.args); strings.Contains(got, "--limit") {
		t.Errorf("kubectl = %q, want no --limit", got)
	}
	entry, _ := services.State.Load()
	if entry.Query.Limit != 2 || slices.Contains(entry.Query.Args, "--limit=2") {
		t.Errorf("saved query = %+v, want its limit 2, apart from kubectl's args", entry.Query)
	}

	out = captureRender(t)
	if err := executeArgv(newGetCommand(services), []string{"pods", "--sort", "-restarts"}); err != nil {
		t.Fatalf("kx get pods --sort -restarts: %v", err)
	}
	if got, want := names(t, services.State), []string{"api", "web-a", "web-b"}; !slices.Equal(got, want) {
		t.Errorf("unlimited indexes resolve to %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "Pods · prod · 3 items") {
		t.Errorf("caption missing 'Pods · prod · 3 items':\n%s", out)
	}
}

// A limit the listing does not reach cuts nothing, and the caption says so
// with a plain count rather than "3 of 3".
func TestGetLimitAboveTheRowsCutsNothing(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	out := captureRender(t)
	if err := executeArgv(newGetCommand(services), []string{"pods", "--limit", "10"}); err != nil {
		t.Fatalf("kx get pods --limit 10: %v", err)
	}
	if got := names(t, services.State); len(got) != 3 {
		t.Errorf("indexes resolve to %v, want all 3 rows", got)
	}
	if !strings.Contains(out.String(), "Pods · prod · 3 items") || strings.Contains(out.String(), " of ") {
		t.Errorf("caption should read 'Pods · prod · 3 items':\n%s", out)
	}
}

// A limit is applied after the term narrows the rows, so it keeps the first
// rows that match rather than matching among the first rows.
func TestGetLimitCutsAfterTheTerm(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services), []string{"pods", "-m", "web", "--limit", "1"}); err != nil {
		t.Fatalf("kx get pods -m web --limit 1: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-b"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if err := executeArgv(newGetCommand(services), []string{"pods", "-m", "web", "--sort", "age", "--limit", "1"}); err != nil {
		t.Fatalf("kx get pods -m web --sort age --limit 1: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-a"}; !slices.Equal(got, want) {
		t.Errorf("sorted indexes resolve to %v, want %v", got, want)
	}
}

// A saved listing that records a limit is listed again cut the same way, by
// the replay a refresh runs, and the command to relist it names the limit:
// relisted without it, the listing holds rows the indexes never did.
func TestGetLimitIsReplayedFromTheSavedQuery(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	entry := state.State{Namespace: "prod", Query: &state.Query{Resource: "pods", Args: []string{}, Sort: "age", Limit: 1}}
	if _, err := replayGet(services, entry, false); err != nil {
		t.Fatalf("replayGet: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-a"}; !slices.Equal(got, want) {
		t.Errorf("replayed listing resolves to %v, want %v", got, want)
	}
	if got, want := relistCommand(entry, false), "kx get pods -n prod --sort=age --limit=1"; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}
}

// Indexes already choose the rows, and a limit beside them cuts the ones
// fetched, as it cuts any listing: redundant, not refused.
func TestGetLimitBesideIndexes(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services), []string{"pods"}); err != nil {
		t.Fatalf("kx get pods: %v", err)
	}
	if err := executeArgv(newGetCommand(services), []string{"pods", "1..3", "--limit", "2"}); err != nil {
		t.Fatalf("kx get pods 1..3 --limit 2: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-b", "api"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}

// Each refusal is made before kubectl is asked.
func TestGetLimitRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pods", "--limit", "0"},
			"Invalid value for '--limit': '0' keeps no rows — give a count of 1 or more."},
		{[]string{"pods", "--limit=-3"},
			"Invalid value for '--limit': '-3' keeps no rows — give a count of 1 or more."},
		{[]string{"pods", "--limit", "2", "--watch"},
			"'--limit' cannot be combined with '--watch' — a watch streams rows as they change, so there is no finished table to cut."},
		{[]string{"pods", "--limit", "2", "-o", "json"},
			"'--limit' cannot be combined with '-o json' — it cuts the table kx draws, and that output is for a program; pipe it through 'head' instead."},
		{[]string{"pods", "--limit", "2", "--no-headers"},
			"'--limit' cannot be combined with '--no-headers' — it cuts the table kx draws, and that output is for a program; pipe it through 'head' instead."},
		{[]string{"secrets", "--limit", "2", "--decode"},
			"'--limit' cannot be combined with '--decode' — it cuts a listing, and '--decode' prints a Secret's data."},
		{[]string{"contexts", "--limit", "2"},
			"kx get contexts does not support '--limit' — only a resource listing can be cut."},
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
}

// A table with no NAME column is one kx cannot cut by row, and printing it
// whole answered a limit nothing had applied. The control is the same columns
// with a NAME among them, which are cut.
func TestGetLimitRefusesATableWithNoNameColumn(t *testing.T) {
	quietRender(t)
	kube := &fakeKubectl{output: "POD     NODE\nweb-b   node-2\napi     node-1\n", namespace: "prod"}
	err := executeArgv(newGetCommand(switchServices(t, kube)),
		[]string{"pods", "-o", "custom-columns=POD:.metadata.name,NODE:.spec.nodeName", "--limit", "1"})
	if want := "'--limit' reads a table by its NAME column, and kubectl's reply has none — " +
		"pipe it through 'head' instead."; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}

	kube = &fakeKubectl{output: "NAME    NODE\nweb-b   node-2\napi     node-1\n", namespace: "prod"}
	services := switchServices(t, kube)
	if err := executeArgv(newGetCommand(services),
		[]string{"pods", "-o", "custom-columns=NAME:.metadata.name,NODE:.spec.nodeName", "--limit", "1"}); err != nil {
		t.Fatalf("NAME column: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-b"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}

// Another cluster's listing is printed, never saved, and a limit cuts it all
// the same. The control is the unlimited reply, which prints both rows.
func TestGetLimitCutsAnotherClustersListing(t *testing.T) {
	kube := &fakeKubectl{output: sortPods, namespace: "prod"}
	stdout, _, err := runCaptured(t, newGetCommand(switchServices(t, kube)), []string{"pods", "--context=b", "--limit", "1"})
	if err != nil {
		t.Fatalf("kx get pods --context=b --limit 1: %v", err)
	}
	if !strings.Contains(stdout, "web-b") || strings.Contains(stdout, "web-a") {
		t.Errorf("want web-b alone:\n%s", stdout)
	}

	stdout, _, err = runCaptured(t, newGetCommand(switchServices(t, kube)), []string{"pods", "--context=b"})
	if err != nil {
		t.Fatalf("kx get pods --context=b: %v", err)
	}
	if !strings.Contains(stdout, "web-a") {
		t.Errorf("control: want web-a printed:\n%s", stdout)
	}
}

// kx top cuts after its percentage columns and its sort, as kx get does, and
// records the limit for a refresh.
func TestTopLimitKeepsTheFirstSortedRows(t *testing.T) {
	pods := &scriptedKubectl{outputs: []string{topPodsFixture, podsJSON}}
	services := topServices(t, pods)
	out := captureRender(t)
	if err := executeArgv(newTopCommand(services), []string{"--sort", "-memory", "--limit", "1"}); err != nil {
		t.Fatalf("kx top --sort -memory --limit 1: %v", err)
	}
	if got, want := names(t, services.State), []string{"web-2"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "1 of 2 items") {
		t.Errorf("caption missing '1 of 2 items':\n%s", out)
	}
	entry, _ := services.State.Load()
	if entry.Query.Limit != 1 {
		t.Errorf("saved query = %+v, want its limit 1", entry.Query)
	}

	err := executeArgv(newTopCommand(topServices(t, &scriptedKubectl{})), []string{"--limit", "0"})
	if want := "Invalid value for '--limit': '0' keeps no rows — give a count of 1 or more."; err == nil || err.Error() != want {
		t.Errorf("kx top --limit 0: err = %v, want %q", err, want)
	}
}

// A sweep's limit keeps the first rows its table shows, and only those are
// numbered, saved and reported: the healthy rows the table hides are not
// among the five, and not saved behind them either. The caption says how many
// it could have shown, while the checked and healthy counts stay the whole
// sweep's. The control is the same sweep without a limit, which saves every
// resource, healthy ones included.
func TestDiagLimitKeepsTheFirstRowsShown(t *testing.T) {
	sweep := []diagnostics.Data{
		healthy(kinds.Deployment, "a-ok", "prod"),
		unhealthy(kinds.StatefulSet, "db", "prod"),
		unhealthy(kinds.Deployment, "z-bad", "prod"),
		healthy(kinds.StatefulSet, "cache", "prod"),
		unhealthy(kinds.Deployment, "m-bad", "prod"),
	}
	var saved []state.State
	command := triageOf(&fakeGatherer{sweep: sweep}, &saved)
	command.Limit = 2
	result, err := command.Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var shown []string
	for i, row := range result.Reports {
		shown = append(shown, row.Name)
		if row.Index != i+1 {
			t.Errorf("row %s carries index %d, want %d", row.Name, row.Index, i+1)
		}
	}
	if want := []string{"db", "z-bad"}; !slices.Equal(shown, want) {
		t.Errorf("rows = %v, want %v", shown, want)
	}
	last := saved[len(saved)-1]
	if last.Resources.Len() != 2 || len(result.All) != 2 {
		t.Errorf("saved %d and reported %d, want the 2 shown", last.Resources.Len(), len(result.All))
	}
	if result.Checked != 5 || result.Healthy != 2 || result.Of != 3 || last.Query.Limit != 2 {
		t.Errorf("checked %d, healthy %d, of %d, query %+v; want 5, 2, 3 and limit 2",
			result.Checked, result.Healthy, result.Of, last.Query)
	}
	out := captureRender(t)
	render.Triage(result)
	for _, want := range []string{"Mixed · prod · 5 checked · 2 of 3 shown", "2 healthy resources not shown"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	saved = nil
	command = triageOf(&fakeGatherer{sweep: sweep}, &saved)
	result, err = command.Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if saved[len(saved)-1].Resources.Len() != 5 || result.Of != 0 {
		t.Errorf("control: saved %d, of %d; want all 5 and no cut", saved[len(saved)-1].Resources.Len(), result.Of)
	}
}

// --fail-on gates on the whole sweep: a critical resource the limit cut is
// still a critical resource. Sorted by name, the warning leads and the limit
// keeps only it. The control is a sweep with nothing critical in it, which
// passes the same gate.
func TestDiagLimitGatesOnTheWholeSweep(t *testing.T) {
	degraded := unhealthy(kinds.Deployment, "a-degraded", "prod")
	degraded.Replicas = &diagnostics.ReplicaHealth{Desired: 2, Ready: 1, Available: 1, Updated: 2}
	var saved []state.State
	command := triageOf(&fakeGatherer{sweep: []diagnostics.Data{
		degraded, unhealthy(kinds.Deployment, "z-down", "prod"),
	}}, &saved)
	command.Sort, command.Limit = "name", 1
	result, err := command.Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.All) != 1 || result.All[0].Verdict >= diagnostics.Critical {
		t.Fatalf("kept %+v, want only the degraded Deployment, short of critical", result.All)
	}
	if err := sweepGate(result, "critical", diagnostics.Critical); err == nil {
		t.Error("the gate passed a sweep whose critical resource the limit cut")
	}

	command = triageOf(&fakeGatherer{sweep: []diagnostics.Data{degraded}}, &saved)
	command.Limit = 1
	if result, err = command.Execute(context.Background(), "prod", false, false); err != nil {
		t.Fatalf("control: %v", err)
	}
	if err := sweepGate(result, "critical", diagnostics.Critical); err != nil {
		t.Errorf("control: gate = %v, want a pass", err)
	}
}

// A sweep's limit is refused beside an index, as its --sort is, and a count
// that keeps nothing is refused before anything is swept.
func TestDiagLimitRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"1", "--limit", "3"},
			"'--limit' cannot be combined with an index — it cuts a namespace sweep. Drop the flag, or drop the index to sweep the namespace instead."},
		{[]string{"--limit", "0"},
			"Invalid value for '--limit': '0' keeps no rows — give a count of 1 or more."},
	} {
		services := switchServices(t, &fakeKubectl{namespace: "prod"})
		err := executeArgv(newDiagnosticCommand(services, "diagnostic", nil), tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx diag %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
	}
}

// A stale index from a limited sweep is refreshed by sweeping again, cut the
// same way, and the command to relist it names the limit.
func TestStaleRefreshSweepsAgainWithItsLimit(t *testing.T) {
	query := &state.Query{Command: state.CommandDiag, Args: []string{"-n", "prod"}, Limit: 1}
	services := staleClusterServices(t, &fakeKubectl{namespace: "prod"}, query,
		brokenDeployment("api", "prod"), brokenDeployment("db", "prod"))
	out := runStale(t, services)
	if !strings.Contains(out, "Mixed · prod · 2 checked · 1 of 2 shown") {
		t.Errorf("output = %q, want the sweep cut to 1 again", out)
	}
	if got := currentQuery(t, services); got == nil || got.Limit != 1 {
		t.Errorf("current query = %+v, want limit 1", got)
	}
	if got, want := relistCommand(state.State{Namespace: "prod", Query: query}, false), "kx diag -n prod --limit=1"; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}
}

// A stale index from a limited kx top listing is refreshed by running kx top
// again, cut the same way.
func TestStaleRefreshRunsKxTopAgainWithItsLimit(t *testing.T) {
	kube := &fakeKubectl{
		output:    "NAME      CPU(cores)   MEMORY(bytes)\napi-new   1m           2Mi\nweb-new   3m           5Mi\n",
		namespace: "prod",
	}
	services := staleServices(t, kube,
		&state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}, Limit: 1})
	out := runStale(t, services)
	if !strings.Contains(out, "1 of 2 items") || strings.Contains(out, "web-new") {
		t.Errorf("output = %q, want kx top cut to api-new again", out)
	}
	if got := currentQuery(t, services); got == nil || got.Limit != 1 {
		t.Errorf("current query = %+v, want limit 1", got)
	}
}
