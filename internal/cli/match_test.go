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
	if _, _, _, err := services.State.Fields(1); err == nil {
		t.Error("index 1 still resolves; the empty listing must replace the -A one")
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
