package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// kubectl has no exit code that distinguishes a missing resource, so the
// message is all there is to go on.
func TestIsNotFoundRecognizesKubectlMessages(t *testing.T) {
	found := []string{
		`Error from server (NotFound): pods "nginx-abc" not found`,
		`pods "nginx" not found`,
	}
	for _, message := range found {
		if !IsNotFound(errors.New(message)) {
			t.Errorf("IsNotFound(%q) = false, want true", message)
		}
	}

	other := []string{
		`Error from server (Forbidden): pods is forbidden`,
		`Unable to connect to the server: dial tcp: i/o timeout`,
	}
	for _, message := range other {
		if IsNotFound(errors.New(message)) {
			t.Errorf("IsNotFound(%q) = true, want false", message)
		}
	}
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
}

func TestIsStaleRecognizesStaleResourceError(t *testing.T) {
	err := StaleResourceError{Kind: kinds.Pod, Name: "nginx"}
	if !isStale(err) {
		t.Error("isStale on StaleResourceError = false, want true")
	}
	if got, want := err.Error(), "Pod/nginx no longer exists"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestIsStaleIgnoresUnrelatedErrors(t *testing.T) {
	if isStale(errors.New("connection refused")) {
		t.Error("isStale on an unrelated error = true, want false")
	}
}

// staleServices seeds a one-pod listing as the current state entry, with the
// supplied kubectl answering the refresh and query deciding whether there is
// anything to replay.
func staleServices(t *testing.T, kube kubectl.Service, query *state.Query) Services {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	if err := store.Save(state.State{
		Resources: state.NewResources([]string{"api-old"}, kinds.Pod),
		Namespace: "prod",
		Query:     query,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	return Services{
		Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default(),
	}
}

// staleCommand is a command that always fails the way a vanished resource does.
func staleCommand(services Services) *cobra.Command {
	return withRefresh(services, &cobra.Command{
		Use: "describe",
		RunE: func(*cobra.Command, []string) error {
			return StaleResourceError{Kind: kinds.Pod, Name: "api-old"}
		},
	})
}

// The refreshed listing is what the user picks their next index from, so it has
// to be the last thing on screen. Rendering it before the error buries it above
// the failure that explains it.
func TestStaleRefreshPrintsTheListingBelowTheError(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}

	failure := strings.Index(out.String(), "api-old no longer exists")
	listing := strings.Index(out.String(), "State was stale")
	if failure < 0 || listing < 0 {
		t.Fatalf("output missing the error or the listing:\n%s", out.String())
	}
	if failure > listing {
		t.Errorf("listing rendered above the error:\n%s", out.String())
	}
}

// The entrypoint prints whatever the command returns, so a failure already
// reported here has to come back marked as reported or it prints twice.
func TestStaleRefreshReportsTheErrorOnlyOnce(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := staleCommand(services)
	err := cmd.RunE(cmd, nil)

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %#v, want SilentError so the entrypoint stays quiet", err)
	}
	if silent.Code != 1 {
		t.Errorf("exit code = %d, want 1", silent.Code)
	}
	if got := strings.Count(out.String(), "api-old no longer exists"); got != 1 {
		t.Errorf("error rendered %d times, want 1:\n%s", got, out.String())
	}
}

// A replay that fails on its own terms has already put its reason on screen.
// Telling the user to run `kx get` on top of that points at the thing that just
// failed.
func TestStaleRefreshStaysQuietWhenTheReplayFails(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{err: errors.New("connection refused")},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	if strings.Contains(out.String(), "to refresh the list") {
		t.Errorf("hinted at a refresh that had just failed:\n%s", out.String())
	}
}

// An entry that no `kx get` produced — a tree walk, or a triage sweep — has no
// query to replay, and running one is genuinely the way forward.
func TestStaleRefreshHintsWhenThereIsNoQueryToReplay(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{output: podsOutput}, nil)

	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	if !strings.Contains(out.String(), "Run 'kx get <resource>' to refresh the list.") {
		t.Errorf("no hint for an entry with nothing to replay:\n%s", out.String())
	}
}

// A probe confirming the resource is gone turns a plain failure into stale
// state; a probe that succeeds leaves the failure alone.
func TestEnsureExists(t *testing.T) {
	gone := &recordingKubectl{probeCode: 1}
	if err := ensureExists(gone, kinds.Pod, "nginx", "prod"); err == nil {
		t.Error("ensureExists on a missing resource returned nil")
	}
	if want := "get Pod nginx -n prod"; joinArgs(gone.probes[0]) != want {
		t.Errorf("probe = %q, want %q", joinArgs(gone.probes[0]), want)
	}

	live := &recordingKubectl{probeCode: 0}
	if err := ensureExists(live, kinds.Pod, "nginx", "prod"); err != nil {
		t.Errorf("ensureExists on a live resource = %v, want nil", err)
	}
}
