package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// runStateJSON runs kx state with args and returns what it printed.
func runStateJSON(t *testing.T, services Services, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newStateCommand(services)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// stateJSONServices is switchServices with the state service stamping a
// context, the way root.go wires it, so saved entries carry one.
func stateJSONServices(t *testing.T, kube *recordingKubectl) Services {
	t.Helper()
	services := switchServices(t, kube)
	services.State.Context = func() string { return "test" }
	return services
}

func mustSave(t *testing.T, services Services, entry state.State) {
	t.Helper()
	if err := services.State.Save(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// The whole document is pinned, not probed field by field: this is a public
// shape, and a field renamed, dropped or added is exactly the change a
// consumer notices and a Contains check does not.
func TestStateJSONPrintsTheCurrentEntry(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"api-7f9", "worker-2c1"}, kinds.Pod),
		Namespace: "prod",
		Query:     &state.Query{Resource: "pods", Args: []string{}, Match: matchOf("a")},
	})

	got, err := runStateJSON(t, services, "--json")
	if err != nil {
		t.Fatalf("kx state --json: %v", err)
	}
	want := `{
  "schemaVersion": 1,
  "context": "test",
  "entry": {
    "position": 1,
    "current": true,
    "context": "test",
    "namespace": "prod",
    "query": {
      "command": "get",
      "resource": "pods",
      "args": [],
      "match": "a"
    },
    "resources": [
      {
        "index": 1,
        "kind": "Pod",
        "name": "api-7f9",
        "namespace": "prod"
      },
      {
        "index": 2,
        "kind": "Pod",
        "name": "worker-2c1",
        "namespace": "prod"
      }
    ]
  }
}
`
	if got != want {
		t.Errorf("kx state --json =\n%s\nwant\n%s", got, want)
	}
}

// Each row's namespace is the one its index resolves into — the same answer
// kx ref gives — so a listing spanning namespaces reports every row's own,
// and a cluster-scoped listing reports none, at either level.
func TestStateJSONRowNamespacesMatchWhatAnIndexResolvesTo(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	mustSave(t, services, state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "api", Kind: kinds.Deployment, Namespace: "prod"},
			{Name: "api", Kind: kinds.Deployment, Namespace: "staging"},
		}),
		AllNamespaces: true,
		Query:         &state.Query{Resource: "deploy", Args: []string{"-A"}},
	})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"node-a"}, kinds.Node),
		Query:     &state.Query{Resource: "nodes", Args: []string{}},
	})

	got, err := runStateJSON(t, services, "--all", "--json")
	if err != nil {
		t.Fatalf("kx state --all --json: %v", err)
	}
	var document stateHistoryDocument
	if err := json.Unmarshal([]byte(got), &document); err != nil {
		t.Fatalf("decoding %q: %v", got, err)
	}
	if len(document.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(document.Entries))
	}

	spanning := document.Entries[0]
	if !spanning.AllNamespaces || spanning.Namespace != "" {
		t.Errorf("-A entry: allNamespaces=%v namespace=%q, want true and none",
			spanning.AllNamespaces, spanning.Namespace)
	}
	for i, want := range []string{"prod", "staging"} {
		if got := spanning.Resources[i].Namespace; got != want {
			t.Errorf("-A row %d namespace = %q, want %q", i+1, got, want)
		}
	}

	nodes := document.Entries[1]
	if nodes.Namespace != "" || nodes.Resources[0].Namespace != "" {
		t.Errorf("cluster-scoped entry carries a namespace: entry %q, row %q",
			nodes.Namespace, nodes.Resources[0].Namespace)
	}
	if !strings.Contains(got, `"kind": "Node"`) || strings.Contains(got, `"namespace": ""`) {
		t.Errorf("want an absent namespace rather than an empty one:\n%s", got)
	}
}

// --all numbers entries the way kx state N takes them and marks exactly the
// cursor as current.
func TestStateJSONAllMarksTheCursor(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	for _, resource := range []string{"pods", "deploy", "svc"} {
		mustSave(t, services, state.State{
			Resources: state.NewResources([]string{"x"}, kinds.Pod),
			Namespace: "prod",
			Query:     &state.Query{Resource: resource, Args: []string{}},
		})
	}
	if _, err := services.State.Navigate(-1); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	got, err := runStateJSON(t, services, "--all", "--json")
	if err != nil {
		t.Fatalf("kx state --all --json: %v", err)
	}
	var document stateHistoryDocument
	if err := json.Unmarshal([]byte(got), &document); err != nil {
		t.Fatalf("decoding %q: %v", got, err)
	}
	for i, entry := range document.Entries {
		if entry.Position != i+1 {
			t.Errorf("entry %d position = %d, want %d", i, entry.Position, i+1)
		}
		if want := i == 1; entry.Current != want {
			t.Errorf("entry %d current = %v, want %v", i+1, entry.Current, want)
		}
	}
}

// kx state N --json does what kx state N does — moves the cursor — and then
// reports the entry it landed on.
func TestStateJSONPositionMovesTheCursor(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	for _, resource := range []string{"pods", "deploy"} {
		mustSave(t, services, state.State{
			Resources: state.NewResources([]string{"x"}, kinds.Pod),
			Namespace: "prod",
			Query:     &state.Query{Resource: resource, Args: []string{}},
		})
	}

	got, err := runStateJSON(t, services, "1", "--json")
	if err != nil {
		t.Fatalf("kx state 1 --json: %v", err)
	}
	var document stateDocument
	if err := json.Unmarshal([]byte(got), &document); err != nil {
		t.Fatalf("decoding %q: %v", got, err)
	}
	if document.Entry == nil || document.Entry.Position != 1 || document.Entry.Query.Resource != "pods" {
		t.Errorf("entry = %+v, want position 1, the pods listing", document.Entry)
	}
	current, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.Query.Resource != "pods" {
		t.Errorf("cursor is on %q after kx state 1 --json, want pods", current.Query.Resource)
	}
}

// A listing an agent took is told apart from the user's own, and a user's
// own carries no flag at all.
func TestStateJSONTagsAnAgentsListing(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"x"}, kinds.Pod), Namespace: "prod",
		Query: &state.Query{Resource: "pods", Args: []string{}},
	})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"y"}, kinds.Deployment), Namespace: "prod",
		Query:  &state.Query{Resource: "deploy", Args: []string{}},
		Source: state.SourceMCP,
	})

	got, err := runStateJSON(t, services, "--all", "--json")
	if err != nil {
		t.Fatalf("kx state --all --json: %v", err)
	}
	var document stateHistoryDocument
	if err := json.Unmarshal([]byte(got), &document); err != nil {
		t.Fatalf("decoding %q: %v", got, err)
	}
	if document.Entries[0].ByAgent || !document.Entries[1].ByAgent {
		t.Errorf("byAgent = %v, %v; want false, true",
			document.Entries[0].ByAgent, document.Entries[1].ByAgent)
	}
	if strings.Count(got, `"byAgent"`) != 1 {
		t.Errorf("want byAgent only on the agent's entry:\n%s", got)
	}
}

// A sweep's entry has no query, and says so as null rather than inventing a
// command it cannot know; a queried entry from a command other than kx get
// names that command.
func TestStateJSONQueryShapes(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"x"}, kinds.Deployment), Namespace: "prod",
	})
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"y"}, kinds.Pod), Namespace: "prod",
		Query: &state.Query{Resource: "pods", Args: []string{}, Command: "top"},
	})

	got, err := runStateJSON(t, services, "--all", "--json")
	if err != nil {
		t.Fatalf("kx state --all --json: %v", err)
	}
	if !strings.Contains(got, `"query": null`) {
		t.Errorf("want a sweep's query as null:\n%s", got)
	}
	if !strings.Contains(got, `"command": "top"`) {
		t.Errorf("want top's entry to name its command:\n%s", got)
	}
}

// Nothing listed yet is an answer, not a failure: a script tells it apart by
// the document, on stdout, with a zero exit.
func TestStateJSONOnAnAbsentStateFile(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--json"}, `"entry": null`},
		{[]string{"--all", "--json"}, `"entries": []`},
	} {
		services := stateJSONServices(t, &recordingKubectl{})
		got, err := runStateJSON(t, services, tc.args...)
		if err != nil {
			t.Errorf("kx state %v with no state: %v", tc.args, err)
		}
		if !strings.Contains(got, tc.want) || !strings.Contains(got, `"schemaVersion": 1`) {
			t.Errorf("kx state %v = %q, want a document with %s", tc.args, got, tc.want)
		}
	}
}

// A listing that found nothing still has an entry, with no rows in it — an
// empty array, not a missing one.
func TestStateJSONEmptyListing(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	mustSave(t, services, state.State{
		Resources: state.NewResources(nil, kinds.Pod), Namespace: "prod",
		Query: &state.Query{Resource: "pods", Args: []string{}},
	})
	got, err := runStateJSON(t, services, "--json")
	if err != nil {
		t.Fatalf("kx state --json: %v", err)
	}
	if !strings.Contains(got, `"resources": []`) {
		t.Errorf("want an empty resources array:\n%s", got)
	}
}

// Like kx ref, this reports what indexes mean without asking the cluster
// anything.
func TestStateJSONMakesNoKubectlCall(t *testing.T) {
	kube := &recordingKubectl{}
	services := stateJSONServices(t, kube)
	mustSave(t, services, state.State{
		Resources: state.NewResources([]string{"x"}, kinds.Pod), Namespace: "prod",
		Query: &state.Query{Resource: "pods", Args: []string{}},
	})
	for _, args := range [][]string{{"--json"}, {"--all", "--json"}, {"1", "--json"}} {
		if _, err := runStateJSON(t, services, args...); err != nil {
			t.Fatalf("kx state %v: %v", args, err)
		}
	}
	if len(kube.runs)+len(kube.interactive)+len(kube.probes) != 0 {
		t.Errorf("kubectl called: runs=%v interactive=%v probes=%v",
			kube.runs, kube.interactive, kube.probes)
	}
}

// The slots are switch screens, not listings a script spends, and --json
// there is refused rather than silently printing the table.
func TestStateJSONRefusesTargets(t *testing.T) {
	services := stateJSONServices(t, &recordingKubectl{})
	got, err := runStateJSON(t, services, "--targets", "--json")
	if err == nil {
		t.Fatalf("kx state --targets --json succeeded with %q, want a refusal", got)
	}
	if !strings.Contains(err.Error(), "--targets") {
		t.Errorf("err = %v, want it to name --targets", err)
	}
}
