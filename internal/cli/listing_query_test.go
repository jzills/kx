package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// queryString renders a saved query for comparison, nil included.
func queryString(query *state.Query) string {
	if query == nil {
		return "<nil>"
	}
	match := "<nil>"
	if query.Match != nil {
		match = *query.Match
	}
	return fmt.Sprintf("command=%q resource=%q args=%q match=%s",
		query.Command, query.Resource, query.Args, match)
}

func wantQuery(command, resource string, args []string, match string) string {
	query := &state.Query{Command: command, Resource: resource, Args: args}
	if match != "" {
		query.Match = &match
	}
	return queryString(query)
}

// A sweep's entry records how it was swept, as a kx get listing records its
// query. Without one, the term that emptied a sweep could not be named, the
// listing could not be refreshed, and kx state --json reported "query":
// null. The scope is recorded as resolved — the namespace swept, not
// whether -n was typed — and --since as typed, so the window comes from the
// configuration in force when it is re-run, as it would typed again.
func TestTriageRecordsTheSweepThatMadeIt(t *testing.T) {
	for _, tc := range []struct {
		name          string
		command       TriageCommand
		namespace     string
		allNamespaces bool
		full          bool
		want          string
	}{
		{"namespace", TriageCommand{}, "prod", false, false,
			wantQuery("diag", "", []string{"-n", "prod"}, "")},
		{"every namespace, narrowed", TriageCommand{Match: "api"}, "prod", true, false,
			wantQuery("diag", "", []string{"-A"}, "api")},
		// --full decides only what the table prints; the listing saved is
		// every resource swept either way.
		{"a window, and --full left out", TriageCommand{Since: "7d"}, "prod", false, true,
			wantQuery("diag", "", []string{"-n", "prod", "--since", "7d"}, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saved []state.State
			command := tc.command
			command.Diagnostics = &fakeGatherer{sweep: []diagnostics.Data{
				unhealthy(kinds.Deployment, "api", "prod"),
			}}
			command.Save = func(entry state.State) error { saved = append(saved, entry); return nil }
			if _, err := command.Execute(context.Background(), tc.namespace, tc.allNamespaces, tc.full); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := queryString(saved[0].Query); got != tc.want {
				t.Errorf("query = %s\n  want %s", got, tc.want)
			}
		})
	}
}

// A tree records its walk: a namespace's or the forest's like a sweep, and
// one resource's by naming the resource at its root, which is what re-walks
// it. A Namespace index is a walk of that namespace, recorded as one.
func TestTreeRecordsTheWalkThatMadeIt(t *testing.T) {
	ctx := context.Background()

	states := &fakeState{}
	command := TreeCommand{Builder: treeFixture(), State: workload("web", kinds.Deployment), Save: states.Save}
	if _, err := command.Execute(ctx, state.Ref{Index: 1}, true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := queryString(states.saved[0].Query),
		wantQuery("tree", "Deployment/web", []string{"-n", "prod"}, ""); got != want {
		t.Errorf("one resource: query = %s\n  want %s", got, want)
	}

	states = &fakeState{}
	command = TreeCommand{
		Builder: treeFixture(), Save: states.Save, Match: "web",
		State: fakeResolver{name: "prod", namespace: "default", kind: kinds.Namespace},
	}
	if _, err := command.Execute(ctx, state.Ref{Index: 1}, true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := queryString(states.saved[0].Query),
		wantQuery("tree", "", []string{"-n", "prod"}, "web"); got != want {
		t.Errorf("a namespace: query = %s\n  want %s", got, want)
	}

	states = &fakeState{}
	command = TreeCommand{Builder: treeFixture(), Save: states.Save}
	if _, _, err := command.ExecuteAllNamespaces(ctx, true); err != nil {
		t.Fatalf("ExecuteAllNamespaces: %v", err)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d entries for the forest, want 1", len(states.saved))
	}
	if got, want := queryString(states.saved[0].Query),
		wantQuery("tree", "", []string{"-A"}, ""); got != want {
		t.Errorf("the forest: query = %s\n  want %s", got, want)
	}
}

// kx top records --no-limits beside kubectl's flags: it decides the columns
// the listing was read from, so a re-run without it would draw another table.
func TestTopRecordsNoLimits(t *testing.T) {
	kube := &fakeKubectl{output: "NAME   CPU(cores)   MEMORY(bytes)\napi   1m   2Mi\n", namespace: "prod"}
	states := &fakeState{}
	if _, _, err := (TopCommand{Kubectl: kube, State: states, Index: indexService()}).
		Execute("", []string{"--sort-by=cpu"}, true); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := queryString(states.saved[0].Query),
		wantQuery("top", "pods", []string{"--sort-by=cpu", "--no-limits"}, ""); got != want {
		t.Errorf("query = %s\n  want %s", got, want)
	}
}

// kx state <TAB> labels a position by what it lists. A sweep has no resource
// to name, so it is named by its command.
func TestCompletionLabelsASweepByItsCommand(t *testing.T) {
	services := completionServices(t)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Deployment), Namespace: "prod",
		Query: &state.Query{Command: "diag", Args: []string{"-n", "prod"}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	candidates := completePosition(services, "")
	if len(candidates) == 0 || candidates[len(candidates)-1] != "2\tdiag in prod" {
		t.Errorf("candidates = %q, want the sweep labelled \"diag in prod\"", candidates)
	}
}
