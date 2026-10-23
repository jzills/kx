package cli

import (
	"slices"
	"strings"
	"testing"

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
