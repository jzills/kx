package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// A listing a --match term narrowed to nothing names the term, as the sweeps
// do (render.NothingMatches). "Pods · prod · none found" says the namespace
// has no pods, when it has two and neither is called zzz.
func TestGetMatchingNothingNamesTheTerm(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod"}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "pods", nil, getOptions{Match: "zzz"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	assertNothingMatches(t, out.String(), "zzz")
}

// A term given where kubectl found nothing at all is still the term the
// listing was asked for, and says so as kx top, the sweeps, kx state and the
// refusal of an index into it all do. The screen alone said "none found",
// because an empty reply was not parsed far enough to carry the term, so the
// one entry was captioned two ways.
func TestGetWithATermInAnEmptyNamespaceNamesTheTerm(t *testing.T) {
	for _, args := range [][]string{nil, {"--context=b"}} {
		kube := &fakeKubectl{output: "", namespace: "prod"}
		services := switchServices(t, kube)

		var out bytes.Buffer
		render.SetOutput(&out, &out, "github-dark")
		if err := runGet(services, "pods", args, getOptions{Match: "api"}); err != nil {
			t.Fatalf("runGet %v: %v", args, err)
		}
		assertNothingMatches(t, out.String(), "api")
	}
}

// kx top -m takes the same rows through its own filter.
func TestTopMatchingNothingNamesTheTerm(t *testing.T) {
	kube := &fakeKubectl{
		outputs:   []string{"", "NAME            CPU(cores)   MEMORY(bytes)\nnginx-abc-xyz   1m           2Mi\n"},
		namespace: "prod",
	}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	cmd := newTopCommand(services)
	cmd.SetArgs([]string{"--no-limits", "-m", "zzz"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("top: %v", err)
	}
	assertNothingMatches(t, out.String(), "zzz")
}

// A listing from another cluster is printed unnumbered, and narrowed all the
// same.
func TestGetFromAnotherClusterMatchingNothingNamesTheTerm(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := switchServices(t, kube)
	stdout, _, err := runCaptured(t, newGetCommand(services), []string{"pods", "--context=b", "-m", "zzz"})
	if err != nil {
		t.Fatalf("kx get pods --context=b -m zzz: %v", err)
	}
	assertNothingMatches(t, stdout, "zzz")
}

// Indexes from an -A listing that span namespaces are fetched one namespace
// at a time and stitched together. A term that matched none of the replies
// printed them all instead — kubectl's own unfiltered tables, as they came —
// and left the -A listing current behind a screen that showed something
// else. It now captions like any listing that matched nothing, and is saved
// like one, so the old indexes stop resolving.
func TestGetSpanningIndexesMatchingNothingNamesTheTerm(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{
		"NAME            READY   STATUS    RESTARTS   AGE\nnginx-abc-xyz   1/1     Running   0          5d\n",
		"NAME            READY   STATUS    RESTARTS   AGE\nredis-def-uvw   1/1     Running   0          3d\n",
	}}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "nginx-abc-xyz", Kind: kinds.Pod, Namespace: "prod"},
			{Name: "redis-def-uvw", Kind: kinds.Pod, Namespace: "staging"},
		}),
		AllNamespaces: true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "pods", []string{"1", "2"}, getOptions{Match: "zzz"}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	assertNothingMatches(t, out.String(), "zzz")
	if strings.Contains(out.String(), "nginx-abc-xyz") {
		t.Errorf("output = %q, printed rows the term did not match", out.String())
	}
	_, _, _, err := services.State.Fields(1)
	if err == nil {
		t.Fatal("index 1 still resolves; the empty listing must replace the -A one")
	}
	// The entry names what it held and the term that emptied it, as the
	// caption did. Saved with no query, it read "Mixed · none found" in kx
	// state and refused an index as "The current listing is empty."
	if want := "nothing in Pods matches 'zzz'"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to name the kind and the term", err)
	}
}

// A fetch by index across namespaces is one kubectl call per namespace,
// which no one invocation replays, so a stale index into one is not run
// again: kx names the listing its indexes came from instead.
func TestAStaleFetchAcrossNamespacesIsNotReplayed(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput}
	out := runStale(t, staleServices(t, kube,
		&state.Query{Command: state.CommandFetch, Resource: "pods", Args: []string{}}))
	if len(kube.calls) > 0 {
		t.Errorf("replayed %v", kube.calls)
	}
	if !strings.Contains(out, "Run 'kx get pods -A' to refresh the list.") {
		t.Errorf("output = %q, want the -A listing named", out)
	}
}

// The listing a stale fetch names is narrowed by the term the fetch was:
// the unnarrowed -A listing numbers its rows differently from the one the
// indexes came from.
func TestAStaleFetchNamesTheListingWithItsTerm(t *testing.T) {
	term := "api"
	out := runStale(t, staleServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Command: state.CommandFetch, Resource: "pods", Args: []string{}, Match: &term}))
	if !strings.Contains(out, "Run 'kx get pods -A -m api' to refresh the list.") {
		t.Errorf("output = %q, want the -A listing named with its term", out)
	}
}

func assertNothingMatches(t *testing.T, out, term string) {
	t.Helper()
	if want := "nothing matches '" + term + "'"; !strings.Contains(out, want) {
		t.Errorf("output = %q\n  missing %q", out, want)
	}
	if strings.Contains(out, "none found") {
		t.Errorf("output = %q, says none found for a listing a term emptied", out)
	}
}
