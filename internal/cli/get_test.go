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
	// outputs answers successive Run calls when set, so a test can drive a
	// command that issues more than one — a relist whose indexes span
	// namespaces has to make one call per namespace. Falls back to output once
	// exhausted.
	outputs []string
	// calls records every Run, where args keeps only the last. Both exist
	// because most tests assert on a single call and reading calls[len-1]
	// everywhere would be noise.
	calls [][]string
	// watchLines is fed to onLine, one call per entry, when Watch runs.
	watchLines []string
	// watchArgs records the args Watch was called with.
	watchArgs []string
	// watchErr is returned by Watch after watchLines is exhausted.
	watchErr error
}

func (f *fakeKubectl) Run(args []string) (string, error) {
	f.args = args
	f.calls = append(f.calls, args)
	if len(f.outputs) > 0 {
		output := f.outputs[0]
		f.outputs = f.outputs[1:]
		return output, f.err
	}
	return f.output, f.err
}
func (f *fakeKubectl) RunInteractive([]string, bool) (int, error) { return 0, nil }
func (f *fakeKubectl) Probe([]string) int                         { return 0 }

func (f *fakeKubectl) Watch(args []string, onLine func(string) error) error {
	f.watchArgs = args
	for _, line := range f.watchLines {
		if err := onLine(line); err != nil {
			return err
		}
	}
	return f.watchErr
}
func (f *fakeKubectl) CurrentContext() string { return "test-context" }
func (f *fakeKubectl) CurrentNamespace() string {
	if f.namespace != "" {
		return f.namespace
	}
	return "default"
}

type fakeState struct {
	saved []state.State
	// named records slot writes separately, so a test can tell the two
	// destinations apart — which is the whole distinction `kx ns` turns on.
	named []state.State
	err   error
}

func (f *fakeState) Save(s state.State) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, s)
	return nil
}

func (f *fakeState) SaveNamed(s state.State) error {
	if f.err != nil {
		return f.err
	}
	f.named = append(f.named, s)
	return nil
}

func newGet(kubectl *fakeKubectl, states *fakeState) GetCommand {
	return GetCommand{Kubectl: kubectl, State: states, Index: index.Service{}}
}

func TestGetIndexesOutputAndSavesState(t *testing.T) {
	kubectl := &fakeKubectl{output: podsOutput, namespace: "prod"}
	states := &fakeState{}

	output, _, err := newGet(kubectl, states).Execute("pods", "", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(strings.Split(output.Text(), "\n")[0], "X") {
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
	_, _, err := newGet(kubectl, &fakeState{}).Execute("pods", "", []string{"-l", "app=web"})
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
		"short flag":         {"-n", "staging"},
		"long flag":          {"--namespace", "staging"},
		"equals":             {"--namespace=staging"},
		"short equals":       {"-n=staging"},
		"attached shorthand": {"-nstaging"},
	} {
		t.Run(name, func(t *testing.T) {
			states := &fakeState{}
			kubectl := &fakeKubectl{output: podsOutput, namespace: "default"}
			if _, _, err := newGet(kubectl, states).Execute("pods", "", args); err != nil {
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
// Shaped like real `kubectl get pods -A` output: NAMESPACE leads, and the same
// workload name recurs across namespaces.
const allNamespacesPodsOutput = "NAMESPACE      NAME       READY   STATUS    RESTARTS   AGE\n" +
	"default        api-7d8f   1/1     Running   0          5d\n" +
	"staging        api-7d8f   1/1     Running   0          3d"

// -A listings index like any other now that a resource carries its own
// namespace. They went unindexed for as long as state had nowhere to record
// which namespace a row came from.
func TestGetAllNamespacesIsIndexed(t *testing.T) {
	for _, flag := range []string{
		"-A", "--all-namespaces", "--all-namespaces=true", "-A=true",
	} {
		states := &fakeState{}
		kubectl := &fakeKubectl{output: allNamespacesPodsOutput}
		output, _, err := newGet(kubectl, states).Execute("pods", "", []string{flag})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.HasPrefix(output.Text(), "X") {
			t.Errorf("%s output was not indexed:\n%s", flag, output)
		}
		if len(states.saved) != 1 {
			t.Fatalf("%s saved %d entries, want 1", flag, len(states.saved))
		}
	}
}

// Each row's own namespace is what makes an -A index resolvable: two rows here
// share a name, so the namespace is the only thing telling them apart.
func TestGetAllNamespacesSavesEachRowsNamespace(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: allNamespacesPodsOutput}

	if _, _, err := newGet(kubectl, states).Execute("pods", "", []string{"-A"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries := states.saved[0].Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("saved %d resources, want 2 (same-named rows must not collapse)", len(entries))
	}
	if entries[0].Name != "api-7d8f" || entries[0].Namespace != "default" {
		t.Errorf("entries[0] = %+v, want api-7d8f in default", entries[0])
	}
	if entries[1].Name != "api-7d8f" || entries[1].Namespace != "staging" {
		t.Errorf("entries[1] = %+v, want api-7d8f in staging", entries[1])
	}
}

// The entry-level namespace stays empty for -A: there is no single namespace the
// listing came from, and filling one in would make Fields' fallback hand it to
// every row that had none.
func TestGetAllNamespacesLeavesTheEntryNamespaceEmpty(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: allNamespacesPodsOutput}

	if _, _, err := newGet(kubectl, states).Execute("pods", "", []string{"-A"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := states.saved[0].Namespace; got != "" {
		t.Errorf("entry namespace = %q, want empty for an -A listing", got)
	}
}

// A single-namespace listing has no NAMESPACE column, so its resources record
// none and the entry's namespace answers for all of them — the shape every
// existing state file already has.
func TestGetSingleNamespaceRecordsNoPerResourceNamespace(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: podsOutput}

	if _, _, err := newGet(kubectl, states).Execute("pods", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, entry := range states.saved[0].Resources.Entries() {
		if entry.Namespace != "" {
			t.Errorf("resource %q recorded namespace %q, want none", entry.Name, entry.Namespace)
		}
	}
}

// "=false" is a request for the ordinary single-namespace listing, so it must
// index like any other. Reading it as "all namespaces" would silently drop the
// indexes the user is about to use.
func TestGetAllNamespacesFalseIsIndexed(t *testing.T) {
	for _, flag := range []string{"--all-namespaces=false", "-A=false"} {
		states := &fakeState{}
		kubectl := &fakeKubectl{output: podsOutput}
		output, _, err := newGet(kubectl, states).Execute("pods", "", []string{flag})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		// Asserted on the index column rather than on inequality with the raw
		// input: a Table carries that input in Raw, so "not equal" would hold
		// even for output that was never numbered.
		if indexOfHeader(output.Headers, "X") < 0 {
			t.Errorf("%s output was not indexed:\n%s", flag, output.Text())
		}
		if len(states.saved) != 1 {
			t.Errorf("%s saved %d states, want 1", flag, len(states.saved))
		}
	}
}

// kubectl takes the last value when a flag is repeated; the recorded namespace
// has to agree with the one the listing actually came from.
func TestGetRecordsTheLastNamespaceWhenRepeated(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: podsOutput, namespace: "default"}
	args := []string{"-n", "first", "-n", "second"}
	if _, _, err := newGet(kubectl, states).Execute("pods", "", args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if states.saved[0].Namespace != "second" {
		t.Errorf("saved namespace = %q, want second", states.saved[0].Namespace)
	}
}

func TestGetFiltersByMatchTerm(t *testing.T) {
	states := &fakeState{}
	kubectl := &fakeKubectl{output: podsOutput}
	output, _, err := newGet(kubectl, states).Execute("pods", "nginx", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(output.Text(), "redis") {
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
	if _, _, err := newGet(kubectl, states).Execute("pods", "nginx", []string{"-n", "prod"}); err != nil {
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
	if _, _, err := newGet(&fakeKubectl{output: podsOutput}, states).Execute("pods", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if states.saved[0].Query.Match != nil {
		t.Errorf("Query.Match = %v, want nil", states.saved[0].Query.Match)
	}
}

// Empty listings must not push a state entry, or `kx back` fills with nothing.
func TestGetEmptyOutputSavesNothing(t *testing.T) {
	states := &fakeState{}
	if _, _, err := newGet(&fakeKubectl{output: ""}, states).Execute("pods", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 0 {
		t.Errorf("saved %d entries for empty output, want 0", len(states.saved))
	}
}

func TestGetPropagatesKubectlError(t *testing.T) {
	kubectl := &fakeKubectl{err: errors.New("error: the server doesn't have a resource type \"widgets\"")}
	_, _, err := newGet(kubectl, &fakeState{}).Execute("widgets", "", nil)
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
	if _, _, err := newGet(&fakeKubectl{output: output}, states).Execute("widgets.example.com", "", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if kind, _ := states.saved[0].Resources.Kind("widget1"); kind != kinds.Kind("widgets.example.com") {
		t.Errorf("kind = %q, want the resource type carried through", kind)
	}
}

// indexService returns the real index service, used where a test exercises the
// full listing path rather than substituting one.
func indexService() Indexer { return index.Service{} }

// An empty listing saves no state, so a caller reading the namespace back out
// of saved state captioned it with the previous entry's. Switching to an empty
// namespace and running `kx get pods` reported the namespace you had left.
func TestGetReturnsTheNamespaceEvenWhenNothingMatched(t *testing.T) {
	kubectl := &fakeKubectl{output: "", namespace: "empty-ns"}
	states := &fakeState{}

	_, namespace, err := newGet(kubectl, states).Execute("pods", "", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if namespace != "empty-ns" {
		t.Errorf("namespace = %q, want empty-ns", namespace)
	}
	if len(states.saved) != 0 {
		t.Errorf("an empty listing saved state: %+v", states.saved)
	}
}

// An explicit -n is the namespace the listing came from, saved or not.
func TestGetReturnsTheExplicitNamespace(t *testing.T) {
	kubectl := &fakeKubectl{output: "", namespace: "current"}
	_, namespace, err := newGet(kubectl, &fakeState{}).
		Execute("pods", "", []string{"-n", "staging"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if namespace != "staging" {
		t.Errorf("namespace = %q, want staging", namespace)
	}
}

// -n/-A are pure kubectl passthrough, parsed by hand, but were never
// registered so they vanished from --help despite being the most-used flags
// on this command.
func TestGetRegistersNamespaceFlags(t *testing.T) {
	cmd := newGetCommand(Services{})
	for _, name := range []string{"namespace", "all-namespaces"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}
