package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

const podsOutput = "NAME             READY   STATUS    RESTARTS   AGE\n" +
	"nginx-abc-xyz    1/1     Running   0          5d\n" +
	"redis-def-uvw    1/1     Running   0          3d"

// fakeKubectl records the arguments it was called with instead of spawning a
// process.
type fakeKubectl struct {
	output    string
	err       error
	args      []string
	namespace string
}

func (f *fakeKubectl) Run(args []string) (string, error) {
	f.args = args
	return f.output, f.err
}
func (f *fakeKubectl) RunInteractive([]string, bool) (int, error) { return 0, nil }
func (f *fakeKubectl) Probe([]string) int                         { return 0 }
func (f *fakeKubectl) CurrentContext() string                     { return "test-context" }
func (f *fakeKubectl) CurrentNamespace() string {
	if f.namespace != "" {
		return f.namespace
	}
	return "default"
}

type fakeState struct {
	saved []state.State
	err   error
}

func (f *fakeState) Save(s state.State) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, s)
	return nil
}

func newGet(kubectl *fakeKubectl, states *fakeState) GetCommand {
	return GetCommand{Kubectl: kubectl, State: states, Index: index.Service{}}
}

func TestGetIndexesOutputAndSavesState(t *testing.T) {
	kubectl := &fakeKubectl{output: podsOutput, namespace: "prod"}
	states := &fakeState{}

	output, err := newGet(kubectl, states).Execute("pods", "", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(strings.Split(output, "\n")[0], "X") {
		t.Errorf("output is not indexed:\n%s", output)
	}
	if len(states.saved) != 1 {
		t.Fatalf("len(saved) = %d, want 1", len(states.saved))
	}

	saved := states.saved[0]
	names := saved.Names()
	if len(names) != 2 || names[0] != "nginx-abc-xyz" || names[1] != "redis-def-uvw" {
		t.Errorf("saved names = %v", names)
	}
	if saved.Namespace != "prod" {
		t.Errorf("saved namespace = %q, want prod", saved.Namespace)
	}
	if kind, _ := saved.Resources.Kind("nginx-abc-xyz"); kind != kinds.Pod {
		t.Errorf("saved kind = %q, want Pod", kind)
	}
}

func TestGetForwardsExtraArgsToKubectl(t *testing.T) {
	kubectl := &fakeKubectl{output: podsOutput}
	_, err := newGet(kubectl, &fakeState{}).Execute("pods", "", []string{"-l", "app=web"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{"get", "pods", "-l", "app=web"}
	if len(kubectl.args) != len(want) {
		t.Fatalf("args = %v, want %v", kubectl.args, want)
	}
	for i := range want {
		if kubectl.args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, kubectl.args[i], want[i])
		}
	}
}

// The saved namespace must be the one the listing came from, not the context's
// current one, or later commands resolve indexes against the wrong namespace.
func TestGetRecordsExplicitNamespace(t *testing.T) {
	for name, args := range map[string][]string{
		"short flag": {"-n", "staging"},
		"long flag":  {"--namespace", "staging"},
		"equals":     {"--namespace=staging"},
	} {
		t.Run(name, func(t *testing.T) {
			states := &fakeState{}
			kubectl := &fakeKubectl{output: podsOutput, namespace: "default"}
			if _, err := newGet(kubectl, states).Execute("pods", "", args); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if states.saved[0].Namespace != "staging" {
				t.Errorf("saved namespace = %q, want staging", states.saved[0].Namespace)
			}
		})
	}
}

// Names aren't unique across namespaces, so -A results are never indexed —
// dead X numbers would resolve to the wrong resource.
func TestGetAllNamespacesIsNotIndexed(t *testing.T) {
	for _, flag := range []string{"-A", "--all-namespaces"} {
		states := &fakeState{}
		kubectl := &fakeKubectl{output: podsOutput}
		output, err := newGet(kubectl, states).Execute("pods", "", []string{flag})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if output != podsOutput {
			t.Errorf("%s output was indexed:\n%s", flag, output)
		}
		if len(states.saved) != 0 {
			t.Errorf("%s saved state, want none", flag)
		}
	}
}

func TestGetFiltersByMatchTerm(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: podsOutput}
	output, err := newGet(kubectl, states).Execute("pods", "nginx", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(output, "redis") {
		t.Errorf("filtered output still contains redis:\n%s", output)
	}
	if names := states.saved[0].Names(); len(names) != 1 || names[0] != "nginx-abc-xyz" {
		t.Errorf("saved names = %v, want only the matching pod", names)
	}
}

// The saved query is what a stale entry is re-run from, so it has to record the
// match term alongside the resource and flags.
func TestGetSavesQueryForRefresh(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: podsOutput}
	if _, err := newGet(kubectl, states).Execute("pods", "nginx", []string{"-n", "prod"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	query := states.saved[0].Query
	if query == nil {
		t.Fatal("Query = nil, want the invocation recorded")
	}
	if query.Resource != "pods" {
		t.Errorf("Query.Resource = %q, want pods", query.Resource)
	}
	if query.Match == nil || *query.Match != "nginx" {
		t.Errorf("Query.Match = %v, want nginx", query.Match)
	}
	if len(query.Args) != 2 || query.Args[0] != "-n" {
		t.Errorf("Query.Args = %v, want [-n prod]", query.Args)
	}
}

func TestGetWithoutMatchLeavesQueryMatchNil(t *testing.T) {
	states := &fakeState{}
	if _, err := newGet(&fakeKubectl{output: podsOutput}, states).Execute("pods", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if states.saved[0].Query.Match != nil {
		t.Errorf("Query.Match = %v, want nil", states.saved[0].Query.Match)
	}
}

// Empty listings must not push a state entry, or `kx back` fills with nothing.
func TestGetEmptyOutputSavesNothing(t *testing.T) {
	states := &fakeState{}
	if _, err := newGet(&fakeKubectl{output: ""}, states).Execute("pods", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 0 {
		t.Errorf("saved %d entries for empty output, want 0", len(states.saved))
	}
}

func TestGetPropagatesKubectlError(t *testing.T) {
	kubectl := &fakeKubectl{err: errors.New("error: the server doesn't have a resource type \"widgets\"")}
	_, err := newGet(kubectl, &fakeState{}).Execute("widgets", "", nil)
	if err == nil {
		t.Fatal("Execute succeeded despite a kubectl failure")
	}
	if !strings.Contains(err.Error(), "widgets") {
		t.Errorf("error = %q, want kubectl's own message", err)
	}
}

func TestGetUnknownResourceKindPassesThrough(t *testing.T) {
	states := &fakeState{}
	output := "NAME      AGE\nwidget1   5d"
	if _, err := newGet(&fakeKubectl{output: output}, states).Execute("widgets.example.com", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if kind, _ := states.saved[0].Resources.Kind("widget1"); kind != kinds.Kind("widgets.example.com") {
		t.Errorf("kind = %q, want the resource type carried through", kind)
	}
}
