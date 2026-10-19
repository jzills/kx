package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jzills/kx/internal/state"
)

// Another cluster's nodes are unnumbered, but a withheld --no-headers is still
// the caller's: kubectl was asked for the header so the term could find the
// NAME column, and it comes off again on the way out.
func TestTopNodesOnAnotherClusterDropsAWithheldHeader(t *testing.T) {
	kubectl := &scriptedKubectl{outputs: []string{nodesOutput}}

	table, _, err := TopCommand{Kubectl: kubectl}.ExecuteNodes(
		"node-a", []string{"--context=b", "--no-headers"})
	if err != nil {
		t.Fatalf("ExecuteNodes: %v", err)
	}
	if strings.Contains(table.Raw, "NAME") {
		t.Errorf("output = %q, want no header row", table.Raw)
	}
	if !strings.Contains(table.Raw, "node-a") || strings.Contains(table.Raw, "node-b") {
		t.Errorf("output = %q, want node-a alone", table.Raw)
	}
}

// A reply that is not a table had no header to drop. Read as one, -o name's
// lines all went, and the match reported nothing found.
func TestGetOnAnotherClusterNarrowsNamesUnderNoHeaders(t *testing.T) {
	kubectl := &fakeKubectl{output: "pod/web-1\npod/db-1"}

	table, _, err := newGet(kubectl, &fakeState{}).Execute(
		"pods", "web", []string{"--context=b", "-o", "name", "--no-headers"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if table.Raw != "pod/web-1" {
		t.Errorf("output = %q, want pod/web-1", table.Raw)
	}
}

// The live table draws a header of its own and reads kubectl's to find its
// columns, so a headerless watch is streamed as kubectl sends it.
func TestWatchWithNoHeadersStreams(t *testing.T) {
	kube := &recordingKubectl{}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	stdout, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-w", "--no-headers"})
	if err != nil {
		t.Fatalf("kx get pods -w --no-headers: %v", err)
	}
	if len(kube.interactive) != 1 || !strings.Contains(joinArgs(kube.interactive[0]), "--no-headers") {
		t.Errorf("streamed = %q, want one kubectl watch carrying --no-headers", kube.interactive)
	}
	if strings.Contains(stdout, "watches can't be indexed") {
		t.Errorf("stdout = %q, want no caption ahead of headerless rows", stdout)
	}
}

// A term can't narrow a stream kx doesn't draw, so it is refused up front,
// naming the flags typed — not discovered at the first row as "no EVENT
// column".
func TestWatchWithNoHeadersRefusesAMatch(t *testing.T) {
	kube := &recordingKubectl{}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	_, _, err := runCaptured(t, newGetCommand(services),
		[]string{"pods", "-w", "--no-headers", "-m", "web"})
	if err == nil || !strings.Contains(err.Error(), "'--watch --no-headers'") {
		t.Fatalf("err = %v, want --match refused beside a headerless watch", err)
	}
	if len(kube.runs)+len(kube.interactive) != 0 {
		t.Errorf("kubectl was called: %q %q", kube.runs, kube.interactive)
	}
}

// Indexes from an -A listing are fetched a namespace at a time, and a term
// needs each reply's header as much as Execute's does.
func TestExecuteGroupsWithholdsNoHeadersForATerm(t *testing.T) {
	kubectl := &fakeKubectl{outputs: []string{
		"NAME    READY   STATUS    RESTARTS   AGE\nweb-1   1/1     Running   0          5d",
		"NAME    READY   STATUS    RESTARTS   AGE\nweb-2   1/1     Running   0          3d\ndb-1    1/1     Running   0          3d",
	}}
	groups := []namespaceGroup{
		{Namespace: "prod", Names: []string{"web-1"}},
		{Namespace: "staging", Names: []string{"web-2", "db-1"}},
	}
	states := &fakeState{}

	table, err := newGet(kubectl, states).ExecuteGroups(
		"pods", "web", groups, []string{"--no-headers"})
	if err != nil {
		t.Fatalf("ExecuteGroups: %v", err)
	}
	for _, call := range kubectl.calls {
		if strings.Contains(joinArgs(call), "--no-headers") {
			t.Errorf("kubectl args = %q, want --no-headers withheld", joinArgs(call))
		}
	}
	if strings.Contains(table.Raw, "NAME") {
		t.Errorf("output = %q, want no header row", table.Raw)
	}
	if !strings.Contains(table.Raw, "web-1") || !strings.Contains(table.Raw, "web-2") ||
		strings.Contains(table.Raw, "db-1") {
		t.Errorf("output = %q, want web-1 and web-2 alone", table.Raw)
	}
	if len(states.saved) != 0 {
		t.Errorf("saved %d entries, want none — headerless rows are never numbered", len(states.saved))
	}
}

// One name answers with the resource, several with a List; either way the
// per-namespace replies come back as the one List kubectl would give.
var groupsForMerge = []namespaceGroup{
	{Namespace: "prod", Names: []string{"web-1"}},
	{Namespace: "staging", Names: []string{"web-2", "db-1"}},
}

func TestExecuteGroupsMergesJSONReplies(t *testing.T) {
	replies := []string{
		`{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "web-1", "annotations": {"u": "a&b"}}}` + "\n",
		`{"apiVersion": "v1", "kind": "List", "items": [` +
			`{"kind": "Pod", "metadata": {"name": "web-2"}},` +
			`{"kind": "Pod", "metadata": {"name": "db-1"}}], "metadata": {"resourceVersion": ""}}` + "\n",
	}
	for _, tc := range []struct {
		term string
		want []string
	}{
		{"", []string{"web-1", "web-2", "db-1"}},
		{"web", []string{"web-1", "web-2"}},
	} {
		kubectl := &fakeKubectl{outputs: append([]string(nil), replies...)}
		table, err := newGet(kubectl, &fakeState{}).ExecuteGroups(
			"pods", tc.term, groupsForMerge, []string{"-o", "json"})
		if err != nil {
			t.Fatalf("term %q: ExecuteGroups: %v", tc.term, err)
		}
		var list struct {
			Kind  string
			Items []struct{ Metadata struct{ Name string } }
		}
		if err := json.Unmarshal([]byte(table.Raw), &list); err != nil {
			t.Fatalf("term %q: output is not one JSON document: %v\n%s", tc.term, err, table.Raw)
		}
		var names []string
		for _, item := range list.Items {
			names = append(names, item.Metadata.Name)
		}
		if list.Kind != "List" || strings.Join(names, " ") != strings.Join(tc.want, " ") {
			t.Errorf("term %q: kind %q, items %q, want a List of %q", tc.term, list.Kind, names, tc.want)
		}
		if tc.term == "" && !strings.Contains(table.Raw, "a&b") {
			t.Errorf("output escaped kubectl's bytes:\n%s", table.Raw)
		}
	}
}

func TestExecuteGroupsMergesYAMLReplies(t *testing.T) {
	replies := []string{
		"apiVersion: v1\nkind: Pod\nmetadata:\n  name: web-1\n",
		"apiVersion: v1\nitems:\n- kind: Pod\n  metadata:\n    name: web-2\n" +
			"- kind: Pod\n  metadata:\n    name: db-1\nkind: List\nmetadata:\n  resourceVersion: \"\"\n",
	}
	for _, tc := range []struct {
		term string
		want []string
	}{
		{"", []string{"web-1", "web-2", "db-1"}},
		{"web", []string{"web-1", "web-2"}},
	} {
		kubectl := &fakeKubectl{outputs: append([]string(nil), replies...)}
		table, err := newGet(kubectl, &fakeState{}).ExecuteGroups(
			"pods", tc.term, groupsForMerge, []string{"-o", "yaml"})
		if err != nil {
			t.Fatalf("term %q: ExecuteGroups: %v", tc.term, err)
		}
		var list struct {
			Kind  string
			Items []struct {
				Metadata struct{ Name string }
			}
		}
		if err := yaml.Unmarshal([]byte(table.Raw), &list); err != nil {
			t.Fatalf("term %q: output is not one YAML document: %v\n%s", tc.term, err, table.Raw)
		}
		var names []string
		for _, item := range list.Items {
			names = append(names, item.Metadata.Name)
		}
		if list.Kind != "List" || strings.Join(names, " ") != strings.Join(tc.want, " ") {
			t.Errorf("term %q: kind %q, items %q, want a List of %q", tc.term, list.Kind, names, tc.want)
		}
	}
}
