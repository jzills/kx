package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// No gathered signals at all is the cheapest OK verdict to build:
// BuildReport finds no findings for it and defaults to healthy.
func healthy(kind kinds.Kind, name, namespace string) diagnostics.Data {
	return diagnostics.Data{Kind: kind, Name: name, Namespace: namespace}
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

// -A sweeps every namespace and indexes what it finds, now that a resource
// carries the namespace it was found in. Two same-named workloads in different
// namespaces must each keep their own number.
func TestTriageAllNamespacesSweepsEverythingAndIndexesIt(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.Deployment, "web", "staging"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "ignored", true, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gatherer.sweptWith != "" {
		t.Errorf("swept %q, want every namespace", gatherer.sweptWith)
	}
	if !result.AllNamespaces {
		t.Error("result is not marked all-namespaces")
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d states, want 1", len(saved))
	}
	entries := saved[0].Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("saved %d resources, want 2 (same-named rows must not collapse)", len(entries))
	}
	if entries[0].Namespace != "prod" || entries[1].Namespace != "staging" {
		t.Errorf("namespaces = %q, %q; want prod, staging",
			entries[0].Namespace, entries[1].Namespace)
	}
}

// The entry namespace stays empty for a cluster-wide sweep, so Fields' fallback
// never hands one namespace's name to a row from another.
func TestTriageAllNamespacesLeavesTheEntryNamespaceEmpty(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	if _, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "ignored", true, false); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(saved) != 1 {
		t.Fatalf("saved %d states, want 1", len(saved))
	}
	if saved[0].Namespace != "" {
		t.Errorf("entry namespace = %q, want empty for a cluster-wide sweep", saved[0].Namespace)
	}
}

func TestTriageScopedSweepSavesStateForItsNamespace(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).
		Execute(context.Background(), "prod", false, false)
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

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "", true, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 2 {
		t.Errorf("reported %d rows, want both namespaces' web", len(result.Reports))
	}
}

// Resource kind is resolved positionally, not by re-searching state by name,
// so a Deployment and a Service both called web within one namespace are both
// indexed correctly rather than one being dropped.
func TestTriageScopedSweepKeepsCrossKindNamesakes(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		unhealthy(kinds.StatefulSet, "web", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 2 {
		t.Errorf("reported %d rows, want both the Deployment and the StatefulSet", len(result.Reports))
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d states, want one", len(saved))
	}
	entries := saved[0].Resources.Entries()
	if len(entries) != 2 {
		t.Fatalf("saved %d entries, want both namesakes indexed", len(entries))
	}
	if entries[0].Name != "web" || entries[0].Kind != kinds.Deployment {
		t.Errorf("entries[0] = %+v, want web/Deployment", entries[0])
	}
	if entries[1].Name != "web" || entries[1].Kind != kinds.StatefulSet {
		t.Errorf("entries[1] = %+v, want web/StatefulSet", entries[1])
	}
}

// --full only changes what the terminal table includes; the HTML report's
// full inventory (result.All) must not depend on it either way.
func TestTriageFullIncludesHealthyInTerminalReports(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "web", "prod"),
		healthy(kinds.Pod, "quiet", "prod"),
	}}
	var saved []state.State

	withoutFull, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(withoutFull.Reports) != 1 {
		t.Errorf("without --full, Reports = %d rows, want 1 (unhealthy only)", len(withoutFull.Reports))
	}
	if len(withoutFull.All) != 2 {
		t.Errorf("All = %d rows, want 2 (every swept resource) regardless of --full", len(withoutFull.All))
	}

	withFull, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(withFull.Reports) != 2 {
		t.Errorf("--full Reports = %d rows, want 2 (every swept resource)", len(withFull.Reports))
	}
	if !withFull.Full {
		t.Error("result.Full was not set for a --full sweep")
	}
}

// Before --full existed, an all-healthy sweep saved no state at all — there
// was nothing unhealthy to index. Now the HTML grid always shows every swept
// resource, so every resource must be indexed too, regardless of --full or
// whether anything was unhealthy — otherwise a healthy row's index in the
// grid would resolve to nothing.
func TestTriageAlwaysIndexesEveryReportRegardlessOfFull(t *testing.T) {
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{
		healthy(kinds.Pod, "quiet", "prod"),
	}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Reports) != 0 {
		t.Errorf("Reports = %d rows, want 0 (nothing unhealthy, --full not set)", len(result.Reports))
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d states, want one — the healthy resource must still be indexed", len(saved))
	}
	entries := saved[0].Resources.Entries()
	if len(entries) != 1 || entries[0].Name != "quiet" {
		t.Errorf("entries = %+v, want the one healthy resource indexed", entries)
	}
}

func TestDiagnosticRegistersFullFlag(t *testing.T) {
	cmd := newDiagnosticCommand(Services{}, "diagnostic", []string{"diag"})
	if cmd.Flags().Lookup("full") == nil {
		t.Error("--full is not registered, so it will not appear in --help")
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

func TestDiagRejectsFullAlongsideAnIndex(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
	if err := cmd.Flags().Set("full", "true"); err != nil {
		t.Fatalf("set --full: %v", err)
	}
	err := cmd.RunE(cmd, []string{"1"})
	if err == nil {
		t.Fatal("--full was accepted alongside an index")
	}
	if !strings.Contains(err.Error(), "cannot be combined with an index") {
		t.Errorf("err = %v", err)
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
		// Reports is the terminal-only shape (unhealthy, or full when --full);
		// All is what the HTML page always uses regardless of --full — set to a
		// different report so a page that reads the wrong field is caught.
		Reports: []diagnostics.Report{{Name: "web", Kind: kinds.Deployment}},
		All:     []diagnostics.Report{{Name: "quiet", Kind: kinds.Pod, Verdict: diagnostics.OK}},
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
	if len(page.Reports) != 1 || page.Reports[0].Name != "quiet" {
		t.Errorf("Reports = %+v, want result.All's one healthy report, not result.Reports — "+
			"the HTML page must show the full inventory regardless of --full", page.Reports)
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
	if !strings.Contains(sink.String(), "nothing to check") {
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
	if !strings.Contains(sink.String(), "nothing to check") {
		t.Errorf("terminal output = %q, want the triage caption to print with --html left off", sink.String())
	}
	if strings.Contains(sink.String(), "serving at") {
		t.Errorf("terminal output = %q, want no serve announcement with --html left off", sink.String())
	}
}

// Driven through RunE with real state, not just the JSON builder directly:
// a unit test of diagnosticJSON alone cannot see whether the command wires
// the index it just resolved through to it, or drops it on the floor — the
// same "a test that calls the helpers directly agrees with itself; only real
// argv proves the wiring" lesson this codebase already has elsewhere.
func TestDiagJSONIndexedRunCarriesTheRealIndex(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "prod"}},
	)
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "api", Kind: kinds.Deployment},
			{Name: "web", Kind: kinds.Deployment},
			{Name: "cache", Kind: kinds.Deployment},
		}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}

	// Index 3, deliberately not 1: a wiring bug that hardcodes or drops the
	// index would still pass a test that only ever asked for the first entry.
	if err := cmd.RunE(cmd, []string{"3"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var document struct {
		Resources []struct {
			Name  string `json:"name"`
			Index int    `json:"index"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(sink.Bytes(), &document); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, sink.String())
	}
	if len(document.Resources) != 1 || document.Resources[0].Index != 3 {
		t.Errorf("resources = %v, want one entry with index 3", document.Resources)
	}
	if document.Resources[0].Name != "cache" {
		t.Errorf("resources[0].name = %q, want cache (index 3's resource)", document.Resources[0].Name)
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

// unhealthyDeployment is a Deployment that wants replicas and has none ready,
// which BuildReport rates Critical — the cheapest fixture that trips a gate.
func unhealthyDeployment() *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}

// --fail-on is a gate for a pipeline and --html is how that pipeline publishes
// what it found, so the two belong together. Returning servePage's result
// *instead of* the gate's made them mutually exclusive: adding --html to a
// green pipeline kept it green whatever the sweep turned up, with nothing
// printed to say the gate had been dropped.
//
// Only driving RunE can see this. sweepGate and verdictGate were always
// correct on their own; the defect was which of them the html branch returned.
func TestDiagSweepWithHTMLStillAppliesTheFailOnGate(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(
		diagnosticHTMLServices(t, unhealthyDeployment()), "diagnostic", []string{"diag"})
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{
		{"namespace", "prod"}, {"html", "true"}, {"no-open", "true"},
		{"fail-on", "critical"},
	} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	var silent SilentError
	err := cmd.RunE(cmd, nil)
	if !errors.As(err, &silent) {
		t.Fatalf("RunE = %v, want --fail-on to exit %d through the html branch", err, findingsExitCode)
	}
	if silent.Code != findingsExitCode {
		t.Errorf("exit code = %d, want %d", silent.Code, findingsExitCode)
	}
}

// The other half of the gate: a clean sweep must still exit 0 with --html set.
// Without this, the test above passes just as well against a gate that always
// fires.
func TestDiagSweepWithHTMLPassesACleanGate(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(diagnosticHTMLServices(t), "diagnostic", []string{"diag"})
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{
		{"namespace", "prod"}, {"html", "true"}, {"no-open", "true"},
		{"fail-on", "critical"},
	} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("RunE = %v, want nil for a sweep with nothing to report", err)
	}
}

// Same regression, single-resource branch: verdictGate has to survive --html
// exactly as sweepGate does.
func TestDiagSingleWithHTMLStillAppliesTheFailOnGate(t *testing.T) {
	quietRender(t)
	services := diagnosticHTMLServices(t, unhealthyDeployment())
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{{Name: "web", Kind: kinds.Deployment}}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("prime state: %v", err)
	}
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	cmd.SetContext(stoppedContext())
	for _, flag := range [][2]string{
		{"html", "true"}, {"no-open", "true"}, {"fail-on", "critical"},
	} {
		if err := cmd.Flags().Set(flag[0], flag[1]); err != nil {
			t.Fatalf("set --%s: %v", flag[0], err)
		}
	}

	var silent SilentError
	err := cmd.RunE(cmd, []string{"1"})
	if !errors.As(err, &silent) {
		t.Fatalf("RunE = %v, want --fail-on to exit %d through the html branch", err, findingsExitCode)
	}
	if silent.Code != findingsExitCode {
		t.Errorf("exit code = %d, want %d", silent.Code, findingsExitCode)
	}
}

func TestDiagnosticRegistersSinceFlag(t *testing.T) {
	cmd := newDiagnosticCommand(Services{}, "diagnostic", []string{"diag"})
	if cmd.Flags().Lookup("since") == nil {
		t.Error("--since is not registered, so it will not appear in --help")
	}
}

// The flag is an override of the setting, not a separate knob: unset means
// whatever config.toml or KX_DIAG_MAX_AGE resolved to.
func TestReportWindowFallsBackToTheConfiguredSetting(t *testing.T) {
	cfg := config.Default()
	cfg.DiagMaxAge = 12 * time.Hour
	got, err := reportWindow("", cfg)
	if err != nil {
		t.Fatalf("eventWindow: %v", err)
	}
	if got != 12*time.Hour {
		t.Errorf("window = %v, want the configured 12h", got)
	}
}

func TestReportWindowFlagOverridesTheSetting(t *testing.T) {
	cfg := config.Default()
	cfg.DiagMaxAge = 12 * time.Hour
	got, err := reportWindow("7d", cfg)
	if err != nil {
		t.Fatalf("eventWindow: %v", err)
	}
	if want := 7 * 24 * time.Hour; got != want {
		t.Errorf("window = %v, want %v", got, want)
	}
}

// --since 0 is how someone gets the old unbounded behaviour back for one run,
// so zero from the flag must not be mistaken for an absent flag.
func TestReportWindowZeroFromTheFlagIsUnlimited(t *testing.T) {
	cfg := config.Default()
	got, err := reportWindow("0", cfg)
	if err != nil {
		t.Fatalf("eventWindow: %v", err)
	}
	if got != 0 {
		t.Errorf("window = %v, want 0 — --since 0 asks for no window", got)
	}
}

func TestReportWindowRejectsAMalformedValue(t *testing.T) {
	if _, err := reportWindow("7 weeks", config.Default()); err == nil {
		t.Fatal("eventWindow accepted '7 weeks'")
	} else if !strings.Contains(err.Error(), "--since") {
		t.Errorf("err = %v, want it to name --since", err)
	}
}

// Parsed before the cluster is read, like --fail-on: a typo should not cost a
// sweep of every namespace before it is reported. Services{} has no client, so
// reaching one would nil-panic rather than return this error.
func TestDiagRejectsAMalformedSinceBeforeReadingTheCluster(t *testing.T) {
	quietRender(t)
	cmd := newDiagnosticCommand(Services{}, "diagnostic", nil)
	if err := cmd.Flags().Set("since", "7 weeks"); err != nil {
		t.Fatalf("set --since: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("a malformed --since was accepted")
	} else if !strings.Contains(err.Error(), "--since") {
		t.Errorf("err = %v", err)
	}
}

// stalePod is a running pod carrying one warning event from three weeks ago —
// the shape the issue describes: nothing wrong now, a verdict stuck at
// "warnings" because of something that happened last month.
func stalePod(name, namespace string) []runtime.Object {
	return []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "app", Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				}},
			},
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "e1", Namespace: namespace},
			Type:           "Warning",
			Reason:         "FailedScheduling",
			Message:        "no nodes available",
			Count:          3,
			LastTimestamp:  metav1.NewTime(time.Now().Add(-21 * 24 * time.Hour)),
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: name, Namespace: namespace},
		},
	}
}

// The whole point, end to end: the window has to reach the gatherer, not just
// be parsed. Asserting on RunE's rendered output is the only way to see that
// the resolved window was actually handed to the diagnostics service.
func TestDiagSweepAppliesTheDefaultEventWindow(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, stalePod("web", "prod")...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if strings.Contains(sink.String(), "FailedScheduling") {
		t.Errorf("a three-week-old warning still drove the sweep:\n%s", sink.String())
	}
}

// ...and --since 0 asks for it back, which is what makes the default a
// default rather than a hard rule.
func TestDiagSweepWithoutAWindowStillReportsAnOldEvent(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, stalePod("web", "prod")...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.Flags().Set("since", "0"); err != nil {
		t.Fatalf("set --since: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "FailedScheduling") {
		t.Errorf("--since 0 dropped an event it was told to keep:\n%s", sink.String())
	}
}

// A saved report has to say what it covers. Without the window on the
// invocation line the page looks like a full account of the namespace while
// silently omitting everything older than a day.
func TestDiagSweepHTMLRecordsTheWindowInTheInvocation(t *testing.T) {
	quietRender(t)
	out := filepath.Join(t.TempDir(), "report.html")
	services := diagnosticHTMLServices(t, stalePod("web", "prod")...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	for name, value := range map[string]string{
		"namespace": "prod", "since": "7d", "out": out,
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(page), "--since 7d") {
		t.Error("the report does not record the event window it was built with")
	}
}

// An unbounded run has no window to record, and a page claiming `--since 0`
// would read as a setting rather than as the absence of one.
func TestDiagSweepHTMLOmitsAnUnlimitedWindow(t *testing.T) {
	quietRender(t)
	out := filepath.Join(t.TempDir(), "report.html")
	services := diagnosticHTMLServices(t, stalePod("web", "prod")...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	for name, value := range map[string]string{
		"namespace": "prod", "since": "0", "out": out,
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(page), "--since") {
		t.Error("an unlimited run recorded a --since it was not given")
	}
}

// settledPod is a running, ready pod whose container thrashed and OOMKilled
// weeks ago and has been fine since. Nothing about it is wrong now, and
// before the window it reported critical forever.
func settledPod(name, namespace string, terminatedAgo time.Duration) []runtime.Object {
	return []runtime.Object{&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true, RestartCount: 21,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Reason: "OOMKilled", ExitCode: 137,
						FinishedAt: metav1.NewTime(time.Now().Add(-terminatedAgo)),
					},
				},
			}},
		},
	}}
}

// The window has to reach a container's own history, not just events — the
// cutoff travels from the flag, through Sweep, onto every Data, and into the
// findings layer.
func TestDiagSweepAppliesTheWindowToContainerHistory(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, settledPod("web", "prod", 21*24*time.Hour)...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	if err := cmd.Flags().Set("namespace", "prod"); err != nil {
		t.Fatalf("set --namespace: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if strings.Contains(sink.String(), "OOMKilled") {
		t.Errorf("a three-week-old OOMKill still drove the sweep:\n%s", sink.String())
	}
}

func TestDiagSweepWithoutAWindowStillReportsOldContainerHistory(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t, settledPod("web", "prod", 21*24*time.Hour)...)
	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	for name, value := range map[string]string{"namespace": "prod", "since": "0"} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(sink.String(), "OOMKilled") {
		t.Errorf("--since 0 dropped history it was told to keep:\n%s", sink.String())
	}
}

// The sweep's caption is where a triage table says what it was allowed to
// see, so the window has to reach the result — every report in one sweep
// carries the same one, which is what makes taking it from a report sound.
func TestTriageResultCarriesTheWindow(t *testing.T) {
	swept := unhealthy(kinds.Deployment, "web", "prod")
	swept.Window = 7 * 24 * time.Hour
	gatherer := &fakeGatherer{sweep: []diagnostics.Data{swept}}
	var saved []state.State

	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Window != 7*24*time.Hour {
		t.Errorf("Window = %v, want the window its reports were gathered under", result.Window)
	}
}

// An empty sweep has no report to take a window from, and no rows to
// qualify — the caption says "nothing to check" and nothing else.
func TestTriageResultWithoutReportsHasNoWindow(t *testing.T) {
	gatherer := &fakeGatherer{}
	var saved []state.State
	result, err := triageOf(gatherer, &saved).Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Window != 0 {
		t.Errorf("Window = %v, want zero", result.Window)
	}
}
