package cli

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// A listing saves the keys of the columns it printed, the names a --sort
// takes, so a later --sort over the kind can be completed without asking
// the cluster: the X kx numbers rows in is not one of them, an -A listing's
// NAMESPACE is, and a listing of several kinds keeps every table's.
func TestListingsSaveTheColumnsTheyPrinted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource string
		output   string
		want     []string
	}{
		{"one kind", "pods", sortPods, []string{"name", "ready", "status", "restarts", "age"}},
		{"every namespace", "pods", podsAllNamespacesOutput,
			[]string{"namespace", "name", "ready", "status", "restarts", "age"}},
		{"several kinds", "deploy,svc", deploySvcOutput, []string{"name", "ready", "up-to-date", "available",
			"age", "type", "cluster-ip", "external-ip", "port"}},
	} {
		services := switchServices(t, &fakeKubectl{output: tc.output, namespace: "prod"})
		var out bytes.Buffer
		render.SetOutput(&out, &out, "github-dark")
		if err := runGet(services, tc.resource, nil, getOptions{}); err != nil {
			t.Fatalf("%s: runGet: %v", tc.name, err)
		}
		entry, err := services.State.Load()
		if err != nil {
			t.Fatalf("%s: Load: %v", tc.name, err)
		}
		if got := strings.Join(entry.Columns, ","); got != strings.Join(tc.want, ",") {
			t.Errorf("%s: saved columns %s, want %s", tc.name, got, strings.Join(tc.want, ","))
		}
	}

	// kx top's percentage columns are kx's own, added after kubectl's.
	kube := &recordingKubectl{outputs: []string{topPodsFixture, podsJSON}}
	services := switchServices(t, kube)
	if _, _, err := (TopCommand{Kubectl: kube, State: services.State, Index: services.Index}).
		Execute("", nil, false); err != nil {
		t.Fatalf("top: %v", err)
	}
	entry, err := services.State.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entry.Columns, ","); got != "name,cpu,memory,cpu%,mem%" {
		t.Errorf("top: saved columns %s, want name,cpu,memory,cpu%%,mem%%", got)
	}
	nodes := &recordingKubectl{outputs: []string{nodesOutput}}
	services = switchServices(t, nodes)
	if _, _, err := (TopCommand{Kubectl: nodes, State: services.State, Index: services.Index}).
		ExecuteNodes("", nil); err != nil {
		t.Fatalf("top nodes: %v", err)
	}
	if entry, err = services.State.Load(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entry.Columns, ","); got != "name,cpu,cpu%,memory,mem%" {
		t.Errorf("top nodes: saved columns %s, want name,cpu,cpu%%,memory,mem%%", got)
	}
}

// completeSort runs kx's completion for a line ending in a --sort value, as
// the shell asks for it, and returns the candidates and the directive.
func completeSort(t *testing.T, services Services, line ...string) ([]string, int) {
	t.Helper()
	root := NewRoot(services, "test")
	var out bytes.Buffer
	root.SetOut(&out)
	if err := Execute(root, append([]string{cobra.ShellCompRequestCmd}, line...)); err != nil {
		t.Fatalf("complete %v: %v", line, err)
	}
	var candidates []string
	directive := -1
	for _, row := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if code, ok := strings.CutPrefix(row, ":"); ok {
			directive, _ = strconv.Atoi(code)
			continue
		}
		candidates = append(candidates, row)
	}
	return candidates, directive
}

// sortSeeded is a state holding a listing of Pods, then one of Deployments
// on top of it, each with the columns kubectl printed for it.
func sortSeeded(t *testing.T) Services {
	t.Helper()
	services := switchServices(t, &recordingKubectl{})
	for _, entry := range []state.State{
		{
			Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
			Query:   &state.Query{Resource: "pods", Args: []string{}},
			Columns: []string{"name", "ready", "status", "restarts", "age"},
		},
		{
			Resources: state.NewResources([]string{"web"}, kinds.Deployment), Namespace: "prod",
			Query:   &state.Query{Resource: "deploy", Args: []string{}},
			Columns: []string{"name", "ready", "up-to-date", "available", "age"},
		},
	} {
		if err := services.State.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	return services
}

// kx get --sort completes the columns of the last listing of the kind named,
// not of the last listing: the Deployments were listed after the Pods, and a
// Pod has no up-to-date column. A kind never listed completes nothing.
func TestSortCompletesTheColumnsOfTheKindsLastListing(t *testing.T) {
	services := sortSeeded(t)
	for _, tc := range []struct {
		line []string
		want string
	}{
		{[]string{"get", "pods", "--sort", ""}, "name,ready,status,restarts,age"},
		{[]string{"po", "--sort", "re"}, "ready,restarts"},
		{[]string{"get", "deployments", "--sort", "up"}, "up-to-date"},
		{[]string{"get", "pods", "--sort", "status,-"}, "status,-name,status,-ready,status,-restarts,status,-age"},
		{[]string{"get", "pods", "-A", "--sort", "n"}, "namespace,name"},
		{[]string{"get", "svc", "--sort", ""}, ""},
	} {
		got, directive := completeSort(t, services, tc.line...)
		if strings.Join(got, ",") != tc.want {
			t.Errorf("kx %s<TAB> = %v, want %s", strings.Join(tc.line, " "), got, tc.want)
		}
		want := int(cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace)
		if directive != want {
			t.Errorf("kx %s<TAB> directive %d, want %d — no space, so a comma can follow", strings.Join(tc.line, " "), directive, want)
		}
	}
}

// A listing's own scope is preferred to the other one's, whose NAMESPACE
// column is put in or taken out: a Pod listing taken with -A completes
// namespace for an -A --sort and leaves it out of a plain one.
func TestSortCompletionPrefersTheListingsOwnScope(t *testing.T) {
	services := switchServices(t, &recordingKubectl{})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), AllNamespaces: true,
		Query:   &state.Query{Resource: "pods", Args: []string{"-A"}},
		Columns: []string{"namespace", "name", "age"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line []string
		want string
	}{
		{[]string{"get", "pods", "-A", "--sort", ""}, "namespace,name,age"},
		{[]string{"get", "pods", "--sort", ""}, "name,age"},
	} {
		if got, _ := completeSort(t, services, tc.line...); strings.Join(got, ",") != tc.want {
			t.Errorf("kx %s<TAB> = %v, want %s", strings.Join(tc.line, " "), got, tc.want)
		}
	}
}

// The commands whose columns are kx's own complete them with no listing
// saved at all; kx top's come from its own listings, never kx get's of the
// same kind.
func TestSortCompletesFixedAndTopColumns(t *testing.T) {
	services := sortSeeded(t)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"node-a"}, kinds.Node),
		Query:     &state.Query{Resource: "nodes", Args: []string{}, Command: state.CommandTop},
		Columns:   []string{"name", "cpu", "cpu%", "memory", "mem%"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line []string
		want string
	}{
		{[]string{"diagnostic", "--sort", ""}, "kind,name,namespace,verdict,top-finding"},
		{[]string{"scan", "--sort", "cr"}, "crit"},
		{[]string{"events", "1", "--sort", ""}, "type,reason,kind,age,message"},
		{[]string{"top", "nodes", "--sort", "c"}, "cpu,cpu%"},
		{[]string{"top", "--sort", ""}, ""},
	} {
		if got, _ := completeSort(t, services, tc.line...); strings.Join(got, ",") != tc.want {
			t.Errorf("kx %s<TAB> = %v, want %s", strings.Join(tc.line, " "), got, tc.want)
		}
	}
}
