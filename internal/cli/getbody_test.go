package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// `kx get pods --watch` used to hang forever: Run() buffers all of kubectl's
// stdout and only returns once the process exits, but `kubectl get --watch`
// never exits on its own. Watch mode can't be indexed either — the listing
// never completes — so it streams straight through via RunInteractive instead,
// like `logs -f` does, bypassing indexing and state entirely.
func TestGetWatchStreamsInsteadOfHanging(t *testing.T) {
	for _, flag := range []string{"--watch", "-w"} {
		t.Run(flag, func(t *testing.T) {
			kube := &recordingKubectl{}
			services := switchServices(t, kube)

			var out bytes.Buffer
			render.SetOutput(&out, &out, "github-dark")

			if err := runGet(services, "pods", []string{flag}, getOptions{}); err != nil {
				t.Fatalf("runGet: %v", err)
			}

			if len(kube.runs) != 0 {
				t.Errorf("Run was called with %v; watch must not go through the buffered path", kube.runs)
			}
			if len(kube.interactive) != 1 {
				t.Fatalf("RunInteractive called %d times, want 1", len(kube.interactive))
			}
			want := []string{"get", "pods", flag}
			if joinArgs(kube.interactive[0]) != joinArgs(want) {
				t.Errorf("interactive args = %v, want %v", kube.interactive[0], want)
			}

			if !strings.Contains(out.String(), "can't be indexed") {
				t.Errorf("output = %q, want a note that watch listings aren't indexed", out.String())
			}

			// Nothing was ever saved, so the state file was never created.
			if _, err := services.State.LoadHistory(); !errors.Is(err, state.ErrNoState) {
				t.Errorf("LoadHistory error = %v, want ErrNoState — a watch listing must not be saved", err)
			}
		})
	}
}

// An indexed argument still resolves to a name before the watch flag short-
// circuits to streaming, so `kx get pods 1 --watch` watches the right pod.
func TestGetWatchResolvesIndexFirst(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := switchServices(t, kube)

	if err := runGet(services, "pods", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	kube.interactive = nil

	if err := runGet(services, "pods", []string{"1", "--watch"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}

	if len(kube.interactive) != 1 {
		t.Fatalf("RunInteractive called %d times, want 1", len(kube.interactive))
	}
	want := []string{"get", "pods", "nginx-abc-xyz", "--watch", "-n", "prod"}
	if joinArgs(kube.interactive[0]) != joinArgs(want) {
		t.Errorf("interactive args = %v, want %v", kube.interactive[0], want)
	}
}
