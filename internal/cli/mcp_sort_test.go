package cli

import (
	"strings"
	"testing"
)

// restartsOutput is a Pod listing whose RESTARTS order is not its name
// order, so a sort by restarts is told apart from kubectl's own order.
const restartsOutput = "NAME   READY   STATUS             RESTARTS   AGE\n" +
	"api    1/1     Running            2          1d\n" +
	"db     0/1     CrashLoopBackOff   14         1d\n" +
	"web    1/1     Running            0          1d\n"

func listedNames(out indexedListOutput) string {
	var names []string
	for _, resource := range out.Resources {
		names = append(names, resource.Name)
	}
	return strings.Join(names, ",")
}

// list_resources orders its rows by the columns of kubectl's table, as kx get
// --sort does, before limit cuts them — so limit keeps the first of that
// order, not of kubectl's. The unsorted call is the control: without it, a
// fixture already in restarts order would pass.
func TestListResourcesSortsByKubectlsColumnsBeforeTheLimit(t *testing.T) {
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{output: restartsOutput, namespace: "prod"}))
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"kind": "pods"}, "api,db,web"},
		{map[string]any{"kind": "pods", "sort": "-restarts"}, "db,api,web"},
		{map[string]any{"kind": "pods", "sort": "-restarts", "limit": 1}, "db"},
		{map[string]any{"kind": "pods", "sort": "status,-name"}, "db,web,api"},
	} {
		var out indexedListOutput
		decodeStructured(t, callTool(t, session, "list_resources", tc.args), &out)
		if got := listedNames(out); got != tc.want {
			t.Errorf("%v: rows %s, want %s", tc.args, got, tc.want)
		}
	}
}

// With --write-listings, a sorted listing is saved as kx get --sort saves it:
// in the sorted order, its sort recorded so a refresh sorts again — and each
// row's index is its place in that order.
func TestWriteListingsListResourcesSortSavesLikeKxGet(t *testing.T) {
	deps := writingDeps(t, &recordingKubectl{output: restartsOutput, namespace: "prod"})
	var out indexedListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "pods", "sort": "-restarts"}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	cli := cliState(t)
	if _, _, err := (GetCommand{
		Kubectl: &recordingKubectl{output: restartsOutput, namespace: "prod"},
		State:   cli, Index: indexService(), Sort: "-restarts",
	}).Execute("pods", "", nil); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
	if entry.Query.Sort != "-restarts" {
		t.Errorf("saved query = %+v, want Sort -restarts", entry.Query)
	}
	if listedNames(out) != "db,api,web" {
		t.Fatalf("rows %s, want db,api,web", listedNames(out))
	}
	for i, row := range out.Resources {
		if row.Index != i+1 {
			t.Errorf("%s carries index %d, want %d", row.Name, row.Index, i+1)
		}
		assertResolves(t, deps.State, row.Index, "Pod", row.Name, "prod")
	}
}

// A sort is refused in the tools' words — 'sort', the argument, not the flag
// '--sort' the command each tool reuses would name — whether the refusal is
// list_resources' own, kx top's or the sweep's.
func TestSortRefusalsNameTheArgument(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		kube *recordingKubectl
		want string
	}{
		{
			"list_resources, unknown column", "list_resources",
			map[string]any{"kind": "pods", "sort": "cpu"},
			&recordingKubectl{output: restartsOutput},
			"Invalid value for 'sort': 'cpu'. Accepted values: name, ready, status, restarts, age.",
		},
		{
			"list_resources, no column", "list_resources",
			map[string]any{"kind": "pods", "sort": ","},
			&recordingKubectl{output: restartsOutput},
			"Invalid value for 'sort': ',' names no column — name each column, separated by commas: 'status,-restarts'.",
		},
		{
			"list_resources, control character", "list_resources",
			map[string]any{"kind": "pods", "sort": "name\x1b[2J"},
			&recordingKubectl{output: restartsOutput},
			"'sort' cannot hold a control character — no column name does.",
		},
		{
			"top, unknown column", "top",
			map[string]any{"sort": "restarts"},
			&recordingKubectl{outputs: []string{topPodsFixture, podsJSON}},
			"Invalid value for 'sort': 'restarts'. Accepted values: name, cpu, memory, cpu%, mem%.",
		},
		{
			"diagnose, unknown column", "diagnose",
			map[string]any{"sort": "age"},
			&recordingKubectl{},
			"Invalid value for 'sort': 'age'. Accepted values: kind, name, namespace, verdict, top-finding.",
		},
	} {
		result := callTool(t, connectMCP(t, mcpDiagDeps(t, tc.kube)), tc.tool, tc.args)
		if !result.IsError || toolText(result) != tc.want {
			t.Errorf("%s: result = %q (error %v), want %q", tc.name, toolText(result), result.IsError, tc.want)
		}
	}
}

// top orders its rows by their columns, CPU% and MEM% among them, before the
// limit — the question "which Pod is closest to its limit" in one call.
func TestTopToolSortsBeforeTheLimit(t *testing.T) {
	names := func(out topOutput) string {
		var names []string
		for _, row := range out.Top.Rows {
			names = append(names, row.Name)
		}
		return strings.Join(names, ",")
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "web-1,web-2"},
		{map[string]any{"sort": "-cpu"}, "web-2,web-1"},
		{map[string]any{"sort": "-cpu", "limit": 1}, "web-2"},
	} {
		kube := &recordingKubectl{outputs: []string{topPodsFixture, podsJSON}}
		var out topOutput
		decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "top", tc.args), &out)
		if got := names(out); got != tc.want {
			t.Errorf("%v: rows %s, want %s", tc.args, got, tc.want)
		}
	}

	kube := &recordingKubectl{outputs: []string{nodesOutput}}
	var out topOutput
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "top",
		map[string]any{"nodes": true, "sort": "-mem%"}), &out)
	if got := names(out); got != "node-b,node-a" {
		t.Errorf("nodes by -mem%%: rows %s, want node-b,node-a", got)
	}
}

// A sweep's sort applies on top of the severity ranking, as kx diag --sort
// does: the critical rows still come first, ordered among themselves.
func TestDiagnoseSweepSortsOnTopOfTheRanking(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		brokenDeployment("api", "prod"), healthyDeployment("web", "prod"),
		brokenDeployment("db", "prod"), brokenDeployment("cache", "prod"))
	session := connectMCP(t, deps)
	names := func(out diagnoseResult) string {
		var names []string
		for _, resource := range out.Diagnosis.Resources {
			names = append(names, resource.Name)
		}
		return strings.Join(names, ",")
	}
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"sort": "name"}, "api,cache,db"},
		{map[string]any{"sort": "-name"}, "db,cache,api"},
		{map[string]any{"sort": "-name", "showHealthy": true}, "web,db,cache,api"},
		{map[string]any{"sort": "verdict,-name", "showHealthy": true}, "web,db,cache,api"},
		{map[string]any{"sort": "-verdict,-name", "showHealthy": true}, "db,cache,api,web"},
	} {
		var out diagnoseResult
		decodeStructured(t, callTool(t, session, "diagnose", tc.args), &out)
		if got := names(out); got != tc.want {
			t.Errorf("%v: resources %s, want %s", tc.args, got, tc.want)
		}
	}
}

// A sort orders a sweep, and a target is one resource, so the two are
// refused together rather than the sort ignored.
func TestDiagnoseRefusesASortBesideATarget(t *testing.T) {
	session := connectMCP(t, mcpDiagDeps(t, &recordingKubectl{}, brokenDeployment("api", "prod")))
	result := callTool(t, session, "diagnose", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}, "sort": "name",
	})
	want := "'sort' applies without a target — a target already names one resource. " +
		"Drop it, or drop the target to sweep."
	if !result.IsError || toolText(result) != want {
		t.Errorf("result = %q (error %v), want the refusal %q", toolText(result), result.IsError, want)
	}
}
