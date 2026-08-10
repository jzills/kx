package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

const watchPodsHeader = "EVENT      NAME             READY   STATUS    RESTARTS   AGE"
const watchPodsAdded = "ADDED      nginx-abc-xyz    1/1     Running   0          5d"
const watchPodsDeleted = "DELETED    nginx-abc-xyz    1/1     Running   0          5d"

// `kx get pods --watch` used to hang forever: Run() buffers all of kubectl's
// stdout and only returns once the process exits, but `kubectl get --watch`
// never exits on its own. It now streams a live-redrawing table instead,
// tracking ADDED/MODIFIED/DELETED via kubectl's --output-watch-events flag,
// rather than the raw passthrough an earlier fix used.
func TestGetWatchDefaultTableUsesLiveRedraw(t *testing.T) {
	for _, flag := range []string{"--watch", "-w"} {
		t.Run(flag, func(t *testing.T) {
			kube := &fakeKubectl{
				watchLines: []string{watchPodsHeader, watchPodsAdded},
			}
			services := switchServices(t, kube)

			var out bytes.Buffer
			render.SetOutput(&out, &out, "github-dark")

			if err := runGet(services, "pods", []string{flag}, getOptions{}); err != nil {
				t.Fatalf("runGet: %v", err)
			}

			if len(kube.args) != 0 {
				t.Errorf("Run was called with %v; watch must not go through the buffered path", kube.args)
			}
			want := []string{"get", "pods", flag, "--output-watch-events"}
			if joinArgs(kube.watchArgs) != joinArgs(want) {
				t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
			}
			// RedrawTable is a no-op off-terminal (internal/render/redraw_test.go
			// covers its actual drawing, and TestWatchRows* in watch_test.go covers
			// the row tracking), so a bytes.Buffer-backed test never sees the row
			// itself — only the note printed unconditionally before the loop starts.
			if !strings.Contains(out.String(), "can't be indexed") {
				t.Errorf("output = %q, want a note that watch listings aren't indexed", out.String())
			}

			if _, err := services.State.LoadHistory(); !errors.Is(err, state.ErrNoState) {
				t.Errorf("LoadHistory error = %v, want ErrNoState — a watch listing must not be saved", err)
			}
		})
	}
}

func TestGetWatchDoesNotDuplicateOutputWatchEvents(t *testing.T) {
	kube := &fakeKubectl{watchLines: []string{watchPodsHeader}}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"--watch", "--output-watch-events"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	count := strings.Count(joinArgs(kube.watchArgs), "--output-watch-events")
	if count != 1 {
		t.Errorf("--output-watch-events appears %d times in %v, want 1", count, kube.watchArgs)
	}
}

func TestGetWatchRemovesDeletedRows(t *testing.T) {
	kube := &fakeKubectl{
		watchLines: []string{watchPodsHeader, watchPodsAdded, watchPodsDeleted},
	}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	// Off-terminal, RedrawTable is a no-op, so this only proves runWatch
	// doesn't error walking an ADDED-then-DELETED sequence down to zero rows.
	// TestWatchRowsDeletedRemoves is the behavioral proof for the removal.
}

// -o json is non-tabular: it keeps the raw-streaming passthrough, since
// re-theming JSON doesn't make sense.
func TestGetWatchNonTabularOutputKeepsPassthrough(t *testing.T) {
	kube := &recordingKubectl{}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch", "-o", "json"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if len(kube.interactive) != 1 {
		t.Fatalf("RunInteractive called %d times, want 1", len(kube.interactive))
	}
	want := []string{"get", "pods", "--watch", "-o", "json"}
	if joinArgs(kube.interactive[0]) != joinArgs(want) {
		t.Errorf("interactive args = %v, want %v", kube.interactive[0], want)
	}
	if !strings.Contains(out.String(), "can't be indexed") {
		t.Errorf("output = %q, want a note that watch listings aren't indexed", out.String())
	}
}

// -A also uses the live table: watchRows keys rows by NAMESPACE/NAME in that
// case (TestWatchRowsKeysByNamespaceAndNameForAllNamespaces is the
// collision-safety proof), so it isn't unindexed-and-passthrough-only.
func TestGetWatchAllNamespacesUsesLiveRedraw(t *testing.T) {
	watchAllNamespacesHeader := "EVENT      NAMESPACE   NAME             READY   STATUS    RESTARTS   AGE"
	watchAllNamespacesAdded := "ADDED      prod        nginx-abc-xyz    1/1     Running   0          5d"
	kube := &fakeKubectl{
		watchLines: []string{watchAllNamespacesHeader, watchAllNamespacesAdded},
	}
	services := switchServices(t, kube)

	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")

	if err := runGet(services, "pods", []string{"--watch", "-A"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "--watch", "-A", "--output-watch-events"}
	if joinArgs(kube.watchArgs) != joinArgs(want) {
		t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
	}
	// The caption itself only renders through RedrawTable, which is a no-op
	// off-terminal — TestWatchNamespaceAllNamespaces in watch_test.go is the
	// direct proof that -A resolves to "all namespaces".
}

// A range argument resolves to every name it spans, the same as typing out
// the equivalent literal indexes, so `kx get pods 1..2` relists both pods.
func TestGetIndexRangeRelist(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod"}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	if err := runGet(services, "pods", []string{"1..2"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "nginx-abc-xyz", "redis-def-uvw", "-n", "prod"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// A kubectl flag value can legitimately contain ".." — JSONPath's recursive
// descent, e.g. -o jsonpath={..metadata.name} — and must reach kubectl
// untouched rather than being mistaken for a range token. Range/int
// recognition is restricted to the leading run specifically so a value like
// this, which never leads, is never inspected for it.
func TestGetPassesThroughDoubleDotFlagValues(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-o", "jsonpath={..metadata.name}"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "-o", "jsonpath={..metadata.name}"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// Indexes only lead; one after a kubectl flag is no longer special-cased and
// reaches kubectl as a literal positional the way any other non-index token
// does — the flip side of TestGetPassesThroughDoubleDotFlagValues, and the
// only way to stop scanning every argument for something index-shaped
// without also mistaking a flag's own value for one.
func TestGetIndexAfterFlagIsNotResolved(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", []string{"-n", "prod", "3"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "-n", "prod", "3"}
	if joinArgs(kube.args) != joinArgs(want) {
		t.Errorf("args = %v, want %v", kube.args, want)
	}
}

// An indexed argument still resolves to a name before the watch flag routes
// to the live table, so `kx get pods 1 --watch` watches the right pod.
func TestGetWatchResolvesIndexFirst(t *testing.T) {
	kube := &fakeKubectl{output: podsOutput, namespace: "prod", watchLines: []string{watchPodsHeader}}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	if err := runGet(services, "pods", []string{"1", "--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	want := []string{"get", "pods", "nginx-abc-xyz", "--watch", "-n", "prod", "--output-watch-events"}
	if joinArgs(kube.watchArgs) != joinArgs(want) {
		t.Errorf("watch args = %v, want %v", kube.watchArgs, want)
	}
}
