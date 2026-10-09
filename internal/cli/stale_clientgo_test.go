package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// gatherFailing is a Gatherer whose Gather fails with err.
type gatherFailing struct{ err error }

func (g gatherFailing) Gather(context.Context, kinds.Kind, string, string) (diagnostics.Data, error) {
	return diagnostics.Data{}, g.err
}

func (g gatherFailing) Sweep(context.Context, string) ([]diagnostics.Data, error) {
	return nil, nil
}

// kx tree and kx diag read the cluster through client-go, and its not-found
// error is not kubectl's, which is the only kind isStale recognised. So an
// index whose resource had gone printed `pods "x" not found` and stopped,
// where every kubectl-backed command — describe, logs, yaml, scan — said it
// no longer exists and refreshed the listing to pick a new index from.
func TestTreeOnAVanishedResourceIsStale(t *testing.T) {
	command := TreeCommand{
		Builder: treeFixture(), State: workload("gone", kinds.Deployment), Save: (&fakeState{}).Save,
	}
	_, err := command.Execute(context.Background(), state.Ref{Index: 1}, true)
	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v (%T), want a StaleResourceError", err, err)
	}
	want := StaleResourceError{Kind: kinds.Deployment, Name: "gone", Namespace: "prod", Ref: state.Ref{Index: 1}}
	if stale != want {
		t.Errorf("stale = %+v, want %+v", stale, want)
	}
	if !isStale(err) {
		t.Error("isStale = false; withRefresh would not refresh it")
	}
}

func TestDiagOnAVanishedResourceIsStale(t *testing.T) {
	gone := apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "gone")
	_, err := DiagnosticCommand{
		State: workload("gone", kinds.Deployment), Diagnostics: gatherFailing{gone},
	}.Execute(context.Background(), state.Ref{Index: 2})
	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v (%T), want a StaleResourceError", err, err)
	}
	want := StaleResourceError{Kind: kinds.Deployment, Name: "gone", Namespace: "prod", Ref: state.Ref{Index: 2}}
	if stale != want {
		t.Errorf("stale = %+v, want %+v", stale, want)
	}
}

// Only the indexed resource's absence is stale state. Something else the
// command read being missing says nothing about the listing, and refreshing
// it would answer an unrelated failure with "no longer exists".
func TestDiagOnSomethingElseMissingIsNotStale(t *testing.T) {
	other := apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "settings")
	_, err := DiagnosticCommand{
		State: workload("web", kinds.Deployment), Diagnostics: gatherFailing{other},
	}.Execute(context.Background(), state.Ref{Index: 1})
	if isStale(err) {
		t.Errorf("err = %v reads as stale; only the indexed resource's absence is", err)
	}
	if err == nil || !strings.Contains(err.Error(), "settings") {
		t.Errorf("err = %v, want the original failure unchanged", err)
	}
}

// kubectl's not-found under a batched read is both kubectl's verdict on one
// resource, which runEach reports under that resource's banner, and stale
// state, which withRefresh reports above the refreshed listing — so it was
// printed twice. A stale failure is left to withRefresh, and stops the batch,
// as the error that can be recovered from has to.
//
// kx labels rather than kx yaml or kx logs, which have the same batched
// shape: those two write a document to stdout, so a stale index reports
// instead of refreshing there — see
// TestAStaleIndexLeavesADocumentOnStdoutAlone.
func TestAStaleKubectlErrorIsReportedOnce(t *testing.T) {
	out := captureRender(t)
	const notFound = `Error from server (NotFound): pods "api-old" not found`
	kube := &recordingKubectl{errs: []error{kubectl.Error{Stderr: notFound}}, output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

	cmd := withRefresh(services, newMetadataReadCommand(services, "labels",
		"Show labels", "Shows every label.", "labels", "LABEL", true))
	cmd.SetArgs([]string{"1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("kx labels on a vanished pod succeeded")
	}
	if got := strings.Count(out.String(), notFound); got != 1 {
		t.Errorf("printed kubectl's error %d times, want once:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "State was stale") {
		t.Errorf("output = %q, want the listing refreshed", out.String())
	}
}
