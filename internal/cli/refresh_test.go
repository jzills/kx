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

// A failed replay still ends with the instruction, because its own reason never
// reaches the screen: kubectl.Run captures both streams, so recoverState
// discards the error rather than printing it.
//
// This used to stay silent, on the theory that the replay had already explained
// itself and a hint would point at the command that just failed. That theory
// held for an interactive command, not for this one — and the silence made this
// the only stale path ending with neither a listing nor an instruction. A
// relist whose saved query names the very resource that went stale (`kx get
// pods 1 2`, then that pod dies) lands here every time.
func TestStaleRefreshStillHintsWhenTheReplayFails(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{err: errors.New("connection refused")},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	if !strings.Contains(out.String(), "Run 'kx get <resource>' to refresh the list.") {
		t.Errorf("a failed replay left the user with no instruction:\n%s", out.String())
	}
}

// A replay that worked needs no instruction — the fresh listing is the answer,
// and telling the user to fetch what is already on screen is noise.
func TestStaleRefreshDoesNotHintWhenTheReplaySucceeds(t *testing.T) {
	out := captureRender(t)
	services := staleServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	if strings.Contains(out.String(), "to refresh the list") {
		t.Errorf("hinted at a refresh that had just succeeded:\n%s", out.String())
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

// A listing from another cluster is recovered exactly like a stale one: the
// saved query is re-run — against the context the user is in now — so a fresh
// listing is on screen to pick from. The original command is never retried,
// which is the property that stops anything reaching the wrong cluster.
func TestIsStaleRecognizesAContextMismatch(t *testing.T) {
	err := state.ContextMismatchError{Index: 1, Listed: "staging", Current: "production"}
	if !isStale(err) {
		t.Error("isStale on a ContextMismatchError = false, want true")
	}
}

// mismatchServices seeds a listing recorded against another context, with the
// service reporting the one the user has since switched to.
func mismatchServices(t *testing.T, kube kubectl.Service, query *state.Query) Services {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	store.Context = func() string { return "staging" }
	if err := store.Save(state.State{
		Resources: state.NewResources([]string{"api-old"}, kinds.Pod),
		Namespace: "prod",
		Query:     query,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	store.Context = func() string { return "production" }
	return Services{
		Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default(),
	}
}

func mismatchCommand(services Services) *cobra.Command {
	return withRefresh(services, &cobra.Command{
		Use: "describe",
		RunE: func(*cobra.Command, []string) error {
			_, _, _, err := services.State.Fields(1)
			return err
		},
	})
}

// A mismatch is not churn, and saying "state was stale" for it describes the
// wrong problem: the listing is intact, it just belongs to another cluster.
func TestContextMismatchRefreshLeadNamesTheContext(t *testing.T) {
	out := captureRender(t)
	services := mismatchServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := mismatchCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("mismatch command returned no error")
	}

	if strings.Contains(out.String(), "State was stale") {
		t.Errorf("a context mismatch reported itself as stale state:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "context") {
		t.Errorf("refresh lead does not mention the context:\n%s", out.String())
	}
}

// The listing has to land below the failure that explains it, the same way a
// stale refresh does — it is what the next index is picked from.
func TestContextMismatchPrintsTheListingBelowTheError(t *testing.T) {
	out := captureRender(t)
	services := mismatchServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := mismatchCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("mismatch command returned no error")
	}

	failure := strings.Index(out.String(), "was listed in context 'staging'")
	listing := strings.Index(out.String(), "nginx-abc-xyz")
	if failure < 0 || listing < 0 {
		t.Fatalf("output missing the error or the refreshed listing:\n%s", out.String())
	}
	if failure > listing {
		t.Errorf("listing rendered above the error:\n%s", out.String())
	}
}

// The refreshed listing belongs to the context the user is in now, not the one
// it was replayed from — otherwise the very next index mismatches again.
func TestContextMismatchRefreshSavesUnderTheCurrentContext(t *testing.T) {
	captureRender(t)
	services := mismatchServices(t, &fakeKubectl{output: podsOutput},
		&state.Query{Resource: "pods", Args: []string{}})

	cmd := mismatchCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("mismatch command returned no error")
	}

	if _, _, _, err := services.State.Fields(1); err != nil {
		t.Errorf("index after refresh = %v, want it to resolve in the current context", err)
	}
}

// A slot mismatch cannot be recovered by replaying the history stack: the
// stack's query relists something else entirely, so `kx ns 2` after a context
// switch would answer with a pods table. The slot has no query of its own, so
// the error names the command that refills it instead of a listing being
// conjured from an unrelated one.
func TestNamespaceSlotMismatchDoesNotReplayTheStackQuery(t *testing.T) {
	out := captureRender(t)
	kube := &fakeKubectl{output: podsOutput}
	services := mismatchServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	if err := services.State.SaveNamed(state.State{
		Resources: state.NewResources([]string{"default", "kube-system"}, kinds.Namespace),
		Context:   "staging",
	}); err != nil {
		t.Fatalf("seed slot: %v", err)
	}

	cmd := withRefresh(services, &cobra.Command{
		Use: "namespace",
		RunE: func(*cobra.Command, []string) error {
			_, _, err := services.State.FieldsNamed(2, kinds.Namespace)
			return err
		},
	})
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("slot mismatch returned no error")
	}

	if strings.Contains(out.String(), "nginx-abc-xyz") {
		t.Errorf("a namespace slot mismatch relisted pods:\n%s", out.String())
	}
	// Returned rather than rendered: nothing was recovered, so the error travels
	// to the entrypoint to be printed like any other failure. A SilentError here
	// would mean the refresh path had swallowed it after showing nothing.
	var silent SilentError
	if errors.As(err, &silent) {
		t.Fatalf("slot mismatch was reported as already-handled, but nothing was shown")
	}
	if !strings.Contains(err.Error(), "kx ns") {
		t.Errorf("error = %q, want it to name the command that refills the slot", err)
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
