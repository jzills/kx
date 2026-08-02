package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/web"
)

// fakeGatherer replays a scripted sweep and records the namespace it was asked
// for — the empty string being how a cluster-wide sweep is spelled.
type fakeGatherer struct {
	sweep     []diagnostics.Data
	sweptWith string
}

func (f *fakeGatherer) Gather(
	context.Context, kinds.Kind, string, string,
) (diagnostics.Data, error) {
	return diagnostics.Data{}, nil
}

func (f *fakeGatherer) Sweep(_ context.Context, namespace string) ([]diagnostics.Data, error) {
	f.sweptWith = namespace
	return f.sweep, nil
}

// A desired-but-unready replica set is the cheapest Critical verdict to build.
func unhealthy(kind kinds.Kind, name, namespace string) diagnostics.Data {
	return diagnostics.Data{
		Kind: kind, Name: name, Namespace: namespace,
		Replicas: &diagnostics.ReplicaHealth{Desired: 2, Ready: 0, Available: 0, Updated: 2},
	}
}

func triageOf(gatherer Gatherer, saved *[]state.State) TriageCommand {
	return TriageCommand{
		Diagnostics: gatherer,
		Save: func(s state.State) error {
			*saved = append(*saved, s)
			return nil
		},
	}
}

// -A sweeps every namespace and saves nothing: names repeat across namespaces,
// so an index would resolve to whichever row happened to be written last.
func TestTriageAllNamespacesSweepsEverythingAndSavesNothing(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "ignored", true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gatherer.sweptWith != "" {
		t.Errorf("swept %q, want every namespace", gatherer.sweptWith)
	}
	if !result.AllNamespaces {
		t.Error("result is not marked all-namespaces, so it would render an X column")
	}
	if len(saved) != 0 {
		t.Errorf("saved %d states, want none", len(saved))
	}
}

func TestTriageScopedSweepSavesStateForItsNamespace(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "prod", false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gatherer.sweptWith != "prod" {
		t.Errorf("swept %q, want prod", gatherer.sweptWith)
	}
	if result.AllNamespaces {
		t.Error("a scoped sweep is marked all-namespaces")
	}
	if len(saved) != 1 || saved[0].Namespace != "prod" {
		t.Fatalf("saved = %+v, want one state for prod", saved)
	}
}

// The collision drop exists only to keep an index from resolving to the wrong
// resource. A cluster-wide sweep has no indexes, so a name shared across two
// namespaces must show both rows rather than silently losing one.
func TestTriageAllNamespacesKeepsNamesakesFromDifferentNamespaces(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.Deployment, "web", "staging"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "", true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 2 {
		t.Errorf("reported %d rows, want both namespaces' web", len(result.Reports))
	}
	if len(result.Dropped) != 0 {
		t.Errorf("dropped %v, want nothing dropped when no state is saved", result.Dropped)
	}
}

// Within one namespace the drop still applies: state is keyed by name alone, so
// a Deployment and a Service both called web cannot both be indexed.
func TestTriageScopedSweepStillDropsCrossKindNamesakes(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.StatefulSet, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 1 {
		t.Errorf("reported %d rows, want the collision dropped", len(result.Reports))
	}
	if len(result.Dropped) != 1 || !strings.Contains(result.Dropped[0], "web") {
		t.Errorf("dropped = %v, want the StatefulSet named", result.Dropped)
	}
}

// Both guards return before any cluster call, which is what lets them run
// against an empty Services{} — a premature call would nil-panic instead.
func TestDiagRejectsAScopeFlagAlongsideAnIndex(t *testing.T) {
	for _, flag := range []struct{ name, value string }{
		{"namespace", "prod"},
		{"all-namespaces", "true"},
	} {
		quietRender(t)
		cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
		if err := cmd.Flags().Set(flag.name, flag.value); err != nil {
			t.Fatalf("set --%s: %v", flag.name, err)
		}
		err := cmd.RunE(cmd, []string{"1"})
		if err == nil {
			t.Fatalf("--%s was accepted alongside an index", flag.name)
		}
		if !strings.Contains(err.Error(), "cannot be combined with an index") {
			t.Errorf("--%s: err = %v", flag.name, err)
		}
	}
}

func TestDiagRejectsNamespaceAndAllNamespacesTogether(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.Flags().Set("all-namespaces", "true"); err != nil {
		t.Fatalf("set --all-namespaces: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("-n and -A were accepted together")
	} else if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("err = %v", err)
	}
}

func TestDiagnosticRegistersHTMLFlags(t *testing.T) {
	cmd := newDiagnosticCommand(Services{}, "diagnostic", []string{"diag"})
	for _, name := range []string{"html", "port", "no-open"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

// sweepPage and resourcePage are the only places a DiagPage's fields are set,
// so a table test over them pins each field to its source without needing a
// live cluster or a served page — dropping AllNamespaces, transposing
// Checked/Healthy, or hard-coding Single wrong all fail one of these directly.

func TestSweepPageMapsEveryFieldFromTheResult(t *testing.T) {
	result := render.TriageResult{
		Namespace:     "prod",
		AllNamespaces: true,
		Checked:       12,
		Healthy:       9,
		Reports:       []diagnostics.Report{{Name: "web", Kind: kinds.Deployment}},
		Dropped:       []string{"Service/web"},
	}
	page := sweepPage(result, web.Meta{Title: "t"})

	if page.Single {
		t.Error("Single = true, want false for a sweep")
	}
	if !page.AllNamespaces {
		t.Error("AllNamespaces not carried from the result — an -A sweep would print index numbers that resolve to nothing")
	}
	if page.Checked != 12 {
		t.Errorf("Checked = %d, want 12 (result.Checked)", page.Checked)
	}
	if page.Healthy != 9 {
		t.Errorf("Healthy = %d, want 9 (result.Healthy) — Checked/Healthy look transposed", page.Healthy)
	}
	if len(page.Reports) != 1 || page.Reports[0].Name != "web" {
		t.Errorf("Reports = %+v, want the one web report", page.Reports)
	}
	if len(page.Dropped) != 1 || page.Dropped[0] != "Service/web" {
		t.Errorf("Dropped = %v, want [Service/web]", page.Dropped)
	}
	if page.Meta.Title != "t" {
		t.Errorf("Meta = %+v, want the meta passed in carried through unchanged", page.Meta)
	}
}

// A scoped (non -A) sweep must not be mistaken for a cluster-wide one: its
// Scope is its own namespace, not the "all namespaces" label.
func TestSweepPageScopedNamespaceKeepsItsOwnName(t *testing.T) {
	result := render.TriageResult{Namespace: "prod", Checked: 1}
	page := sweepPage(result, web.Meta{})
	if page.AllNamespaces {
		t.Error("AllNamespaces = true for a scoped sweep")
	}
	if page.Scope != "prod" {
		t.Errorf("Scope = %q, want prod", page.Scope)
	}
}

func TestResourcePageIsSingleWithExactlyOneReport(t *testing.T) {
	report := diagnostics.Report{Name: "web", Kind: kinds.Deployment, Namespace: "prod"}
	page := resourcePage(report, web.Meta{Title: "t"})

	if !page.Single {
		t.Error("Single = false, want true — a single-resource page must render inline, not as a collapsed sweep row")
	}
	if len(page.Reports) != 1 || page.Reports[0].Name != "web" {
		t.Errorf("Reports = %+v, want exactly the one report", page.Reports)
	}
	if page.Scope != "prod" {
		t.Errorf("Scope = %q, want the report's own namespace", page.Scope)
	}
	if page.Meta.Title != "t" {
		t.Errorf("Meta = %+v, want the meta passed in carried through unchanged", page.Meta)
	}
}

// diagnosticHTMLServices builds a Services that can drive newDiagnosticCommand's
// RunE all the way through a real (fake-clientset) Gather/Sweep, rather than
// stopping at the early guards the way Services{} does elsewhere in this file.
// The fixtures below are deliberately minimal: their only job is to let
// Sweep/Gather return without error so RunE reaches the html branch.
func diagnosticHTMLServices(t *testing.T, objects ...runtime.Object) Services {
	t.Helper()
	return Services{
		State:  &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config: config.Default(),
		Kubernetes: func() (kubernetes.Interface, error) {
			return fake.NewSimpleClientset(objects...), nil
		},
	}
}

// stoppedContext is already Done, standing in for the moment a user presses
// Ctrl-C: web.Serve binds its listener, announces the URL, sees ctx.Done()
// immediately in its select, and shuts back down — the same clean-stop path,
// just without a wall-clock wait. context.WithCancel's cancellation reaches an
// already-cancelled parent synchronously (propagateCancel checks p.err before
// returning), so this is deterministic rather than a race against a goroutine.
func stoppedContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// Moving the "if !htmlOpts.Enabled { return nil }" gate above render.Triage
// would make --html silently swallow the terminal table the command always
// printed — exactly the "adds, never replaces" regression the commit message
// is about. Only driving RunE for real pins the *order* of those two
// statements; asserting on the extracted TriageResult/Report cannot see it.
func TestDiagSweepWithHTMLStillPrintsTheTerminalTriage(t *testing.T) {
	sink := captureRender(t)
	cmd := newDiagnosticCommand(diagnosticHTMLServices(t), "diagnostic", []string{"diag"})
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{
		{"namespace", "empty"}, {"html", "true"}, {"no-open", "true"},
	} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "checked") {
		t.Errorf("terminal output = %q, want the triage caption to still print with --html set", sink.String())
	}
}

// Same regression, single-resource branch: render.Diagnostic must still fire
// before the html gate, even though the Report is about to be re-rendered as
// a page a moment later.
func TestDiagSingleWithHTMLStillPrintsTheTerminalReport(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
	})
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{{Name: "web", Kind: kinds.Deployment}}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{{"html", "true"}, {"no-open", "true"}} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, []string{"1"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Deployment/web") {
		t.Errorf("terminal output = %q, want the diagnostic report to still print with --html set", sink.String())
	}
}

// The two tests above set --html=true, and moving the gate above the render
// call is invisible from there: when Enabled is true the gate never returns
// early either way, so the render call runs regardless of which side of the
// gate it sits on. The mutation only shows up on the far more common path —
// --html left off — where the moved gate now returns before the render call
// ever runs. These two pin that path instead.
func TestDiagSweepWithoutHTMLStillPrintsTheTerminalTriage(t *testing.T) {
	sink := captureRender(t)
	cmd := newDiagnosticCommand(diagnosticHTMLServices(t), "diagnostic", []string{"diag"})
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("namespace", "empty"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "checked") {
		t.Errorf("terminal output = %q, want the triage caption to print with --html left off", sink.String())
	}
	if strings.Contains(sink.String(), "serving at") {
		t.Errorf("terminal output = %q, want no serve announcement with --html left off", sink.String())
	}
}

func TestDiagSingleWithoutHTMLStillPrintsTheTerminalReport(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
	})
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{{Name: "web", Kind: kinds.Deployment}}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	cmd.SetContext(context.Background())

	if err := cmd.RunE(cmd, []string{"1"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "Deployment/web") {
		t.Errorf("terminal output = %q, want the diagnostic report to print with --html left off", sink.String())
	}
	if strings.Contains(sink.String(), "serving at") {
		t.Errorf("terminal output = %q, want no serve announcement with --html left off", sink.String())
	}
}
