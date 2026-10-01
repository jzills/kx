package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/spf13/cobra"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// A matched sweep is the sweep of what matched: the count, the rows, the
// document and the saved indexes all cover those resources and nothing else,
// so index 1 is the first row on screen, not the first row of the unfiltered
// sweep.
func TestTriageMatchNarrowsEverythingTheSweepProduces(t *testing.T) {
	var saved []state.State
	command := triageOf(&fakeGatherer{sweep: []diagnostics.Data{
		unhealthy(kinds.Deployment, "worker", "prod"),
		unhealthy(kinds.Deployment, "api", "prod"),
		healthy(kinds.Deployment, "API-cache", "prod"),
	}}, &saved)
	command.Match = "api"

	result, err := command.Execute(context.Background(), "prod", false, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Checked != 2 || result.Healthy != 1 || result.Match != "api" {
		t.Errorf("Checked=%d Healthy=%d Match=%q, want 2, 1, api",
			result.Checked, result.Healthy, result.Match)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(saved))
	}
	if got := strings.Join(saved[0].Names(), ","); got != "api,API-cache" {
		t.Errorf("saved rows = %s, want api,API-cache in severity order", got)
	}

	document, err := triageJSON(result)
	if err != nil {
		t.Fatalf("triageJSON: %v", err)
	}
	var decoded diagnosticDocument
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if decoded.Match != "api" || len(decoded.Resources) != 2 || decoded.Resources[0].Index != 1 {
		t.Errorf("document match=%q resources=%d first index=%d, want api, 2, 1",
			decoded.Match, len(decoded.Resources), decoded.Resources[0].Index)
	}
}

// The term is on the rows the sweep reports, never on the objects it reads:
// pods are claimed by their owners across the whole namespace, so a term that
// names a Deployment's pod but not the Deployment matches nothing, rather
// than turning the pod loose as an orphan row of its own.
func TestDiagMatchFiltersRowsAfterOwnersClaimTheirPods(t *testing.T) {
	sink := captureRender(t)
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: types.UID("d")},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Name: "web-5c4", Namespace: "prod", UID: types.UID("rs"),
		OwnerReferences: []metav1.OwnerReference{{UID: "d", Kind: "Deployment", Name: "web"}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "web-5c4-api", Namespace: "prod", UID: types.UID("p"),
		OwnerReferences: []metav1.OwnerReference{{UID: "rs", Kind: "ReplicaSet", Name: "web-5c4"}},
	}}
	services := diagnosticHTMLServices(t, deployment, replicaSet, pod)

	cmd := newDiagnosticCommand(services, "diagnostic", []string{"diag"})
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"-n", "prod", "-m", "api", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx diag -m api: %v", err)
	}
	var decoded diagnosticDocument
	if err := json.Unmarshal(sink.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding %q: %v", sink.String(), err)
	}
	if decoded.Checked != 0 {
		t.Errorf("checked %d resources, want 0 — the pod belongs to web, which does not match: %+v",
			decoded.Checked, decoded.Resources)
	}
}

// Nothing matching is its own answer, naming the term, rather than the
// empty-namespace caption: the namespace may be full.
func TestTriageCaptionNamesATermThatMatchedNothing(t *testing.T) {
	sink := captureRender(t)
	render.Triage(render.TriageResult{Namespace: "prod", Match: "api"})
	if got := sink.String(); !strings.Contains(got, "nothing matches 'api'") ||
		strings.Contains(got, "nothing to check") {
		t.Errorf("caption = %q, want it to say nothing matches 'api'", got)
	}
}

// An index already names one resource; a term beside it has nothing to narrow.
func TestSweepMatchIsRefusedBesideAnIndex(t *testing.T) {
	for name, build := range map[string]func(Services) *cobra.Command{
		"diag": func(s Services) *cobra.Command {
			return newDiagnosticCommand(s, "diagnostic", []string{"diag"})
		},
		"tree": newTreeCommand,
		"scan": newScanCommand,
	} {
		quietRender(t)
		services := diagnosticHTMLServices(t)
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"api"}, kinds.Deployment), Namespace: "prod",
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		cmd := build(services)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{"1", "-m", "api"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--match") ||
			!strings.Contains(err.Error(), "index") {
			t.Errorf("kx %s 1 -m api: err = %v, want a refusal naming --match and the index", name, err)
		}
	}
}

const matchScanItems = `{"items":[
  {"kind":"Deployment","metadata":{"name":"api"},
   "spec":{"template":{"spec":{"containers":[{"image":"api:v1"}]}}}},
  {"kind":"Pod","metadata":{"name":"API-7f9"},"spec":{"containers":[{"image":"api:v1"},{"image":"envoy:1"}]}},
  {"kind":"Deployment","metadata":{"name":"worker"},
   "spec":{"template":{"spec":{"containers":[{"image":"worker:v2"}]}}}}
]}`

// A workload the term leaves out never has its images read, so its image is
// never handed to a scanner.
func TestCollectMatchSkipsWorkloadsBeforeReadingTheirImages(t *testing.T) {
	command := ScanCommand{
		Kubectl: &fakeKubectl{output: matchScanItems}, Scanner: &fakeScanner{}, Status: noStatus,
	}
	images, err := command.Collect(scanScope{Namespace: "prod", Match: "api"}, "scout")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := strings.Join(images, ","); got != "api:v1,envoy:1" {
		t.Errorf("images = %s, want api:v1,envoy:1 — worker:v2 belongs to a workload that did not match", got)
	}
}

func scanMatchServices(t *testing.T, output string) (Services, *fakeScanner) {
	t.Helper()
	scanner := &fakeScanner{}
	return Services{
		Kubectl: &fakeKubectl{namespace: "prod", output: output},
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config:  config.Default(),
		Scanner: scanner,
	}, scanner
}

// Driven through the command so the flag is seen to reach Collect, and the
// document to say what the sweep was narrowed by.
func TestScanMatchReachesTheSweepAndTheDocument(t *testing.T) {
	sink := captureRender(t)
	services, scanner := scanMatchServices(t, matchScanItems)
	cmd := newScanCommand(services)
	cmd.SetArgs([]string{"-m", "worker", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx scan -m worker --json: %v", err)
	}
	var decoded scanDocument
	if err := json.Unmarshal(sink.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding %q: %v", sink.String(), err)
	}
	if decoded.Match != "worker" || len(decoded.Images) != 1 || decoded.Images[0].Image != "worker:v2" {
		t.Errorf("document = %+v, want match worker and only worker:v2", decoded)
	}
	if scanner.calls != 1 {
		t.Errorf("scanner ran %d times, want once — for worker:v2 alone", scanner.calls)
	}
}

// A term that matched nothing says so, not "no images found", which reads as
// a namespace with nothing running in it.
func TestScanMatchThatFindsNothingSaysSo(t *testing.T) {
	sink := captureRender(t)
	services, _ := scanMatchServices(t, matchScanItems)
	cmd := newScanCommand(services)
	cmd.SetArgs([]string{"-m", "absent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx scan -m absent: %v", err)
	}
	if got := sink.String(); !strings.Contains(got, "nothing matches 'absent'") {
		t.Errorf("output = %q, want it to say nothing matches 'absent'", got)
	}
}

// matchForest is two namespaces: api (owning a ReplicaSet and its pod) and
// worker in prod, and web alone in staging.
func matchForest() *fake.Clientset {
	return fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "staging"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod", UID: "d-api"}},
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Name: "api-5c4", Namespace: "prod", UID: "rs-api",
			OwnerReferences: []metav1.OwnerReference{{UID: "d-api", Kind: "Deployment", Name: "api"}},
		}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "api-5c4-x", Namespace: "prod", UID: "p-api",
			OwnerReferences: []metav1.OwnerReference{{UID: "rs-api", Kind: "ReplicaSet", Name: "api-5c4"}},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "prod", UID: "d-worker"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "staging", UID: "d-web"}},
	)
}

// A matched root keeps everything it owns, and the numbering runs 1..n over
// what is shown, so the saved listing is exactly the tree on screen.
func TestTreeMatchKeepsMatchedRootsWholeAndNumbersThem(t *testing.T) {
	var saved []state.State
	command := TreeCommand{
		Builder: graph.Builder{Client: matchForest()}, Match: "API",
		Save: func(s state.State) error { saved = append(saved, s); return nil },
	}
	node, err := command.ExecuteNamespace(context.Background(), "prod", true)
	if err != nil {
		t.Fatalf("ExecuteNamespace: %v", err)
	}
	if len(node.Children) != 1 || node.Children[0].Name != "api" || node.Children[0].Index != 1 {
		t.Fatalf("roots = %+v, want Deployment/api alone, numbered 1", node.Children)
	}
	if len(saved) != 1 || strings.Join(saved[0].Names(), ",") != "api,api-5c4,api-5c4-x" {
		t.Errorf("saved = %+v, want api and everything it owns", saved)
	}
}

// Roots, not every node: a term that names only a pod matches nothing, rather
// than pruning its siblings out from under their ReplicaSet.
func TestTreeMatchIsOnRootsNotOnWhatTheyOwn(t *testing.T) {
	command := TreeCommand{
		Builder: graph.Builder{Client: matchForest()}, Match: "5c4-x",
		Save: func(state.State) error { return nil },
	}
	node, err := command.ExecuteNamespace(context.Background(), "prod", true)
	if err != nil {
		t.Fatalf("ExecuteNamespace: %v", err)
	}
	if graph.HasWorkloads(node) {
		t.Errorf("roots = %+v, want none — no root is called 5c4-x", node.Children)
	}
	if len(node.Children) != 1 || node.Children[0].Label != "(no workloads matching '5c4-x')" {
		t.Errorf("children = %+v, want the placeholder naming the term", node.Children)
	}
}

// -A narrowed by a term shows only the namespaces it hit, numbered from 1.
func TestTreeMatchAcrossNamespacesDropsTheOnesItMissed(t *testing.T) {
	command := TreeCommand{Builder: graph.Builder{Client: matchForest()}, Match: "web"}
	roots, resources, err := command.ExecuteAllNamespaces(context.Background(), true)
	if err != nil {
		t.Fatalf("ExecuteAllNamespaces: %v", err)
	}
	if len(roots) != 1 || roots[0].Name != "staging" {
		t.Fatalf("roots = %+v, want staging alone", roots)
	}
	if len(resources) != 1 || roots[0].Children[0].Index != 1 {
		t.Errorf("resources = %+v, want web alone, numbered 1", resources)
	}
}

// Driven through the command: the flag reaches the walk, the document says
// what it was narrowed by, and a forest the term emptied says so on its
// banner rather than printing nothing under it.
func TestTreeMatchThroughTheCommand(t *testing.T) {
	sink := captureRender(t)
	services := diagnosticHTMLServices(t)
	services.Kubernetes = func() (kubernetes.Interface, error) { return matchForest(), nil }
	cmd := newTreeCommand(services)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"-A", "-m", "worker", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx tree -A -m worker --json: %v", err)
	}
	var decoded treeDocument
	if err := json.Unmarshal(sink.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding %q: %v", sink.String(), err)
	}
	if decoded.Match != "worker" || len(decoded.Roots) != 1 || decoded.Roots[0].Name != "prod" {
		t.Errorf("document = %+v, want match worker and prod alone", decoded)
	}

	sink.Reset()
	cmd = newTreeCommand(services)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"-A", "-m", "absent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("kx tree -A -m absent: %v", err)
	}
	if got := sink.String(); !strings.Contains(got, "nothing matches 'absent'") {
		t.Errorf("output = %q, want the banner to say nothing matches 'absent'", got)
	}
}

// A term that matched nothing says so on the HTML page too, not only in the
// terminal: the page said "0 checked" or "0 images", the reading of an empty
// namespace the terminal caption was changed to avoid, while the scope may be
// full of resources that didn't match. Driven through each command to its
// --out file, since the page is built from what the command hands it.
//
// Anchored to the closing tag, and to html/template's escaping of the quotes,
// so the assertion is on the caption's markup and nothing else on the page.
func TestSweepPagesNameATermThatMatchedNothing(t *testing.T) {
	const want = "nothing matches &#39;absent&#39;</p>"
	for _, tc := range []struct {
		name  string
		empty string
		run   func(t *testing.T, out string) error
	}{
		{"diag", "0 checked", func(t *testing.T, out string) error {
			cmd := newDiagnosticCommand(diagnosticHTMLServices(t), "diagnostic", []string{"diag"})
			cmd.SetContext(context.Background())
			cmd.SetArgs([]string{"-n", "prod", "-m", "absent", "--out", out})
			return cmd.Execute()
		}},
		{"scan", "0 images</p>", func(t *testing.T, out string) error {
			services, _ := scanMatchServices(t, matchScanItems)
			cmd := newScanCommand(services)
			cmd.SetArgs([]string{"-m", "absent", "--out", out})
			return cmd.Execute()
		}},
		{"tree -A", "all namespaces</p>", func(t *testing.T, out string) error {
			services := diagnosticHTMLServices(t)
			services.Kubernetes = func() (kubernetes.Interface, error) { return matchForest(), nil }
			cmd := newTreeCommand(services)
			cmd.SetContext(context.Background())
			cmd.SetArgs([]string{"-A", "-m", "absent", "--out", out})
			return cmd.Execute()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quietRender(t)
			out := filepath.Join(t.TempDir(), "report.html")
			if err := tc.run(t, out); err != nil {
				t.Fatalf("kx %s -m absent --out: %v", tc.name, err)
			}
			page, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading the page: %v", err)
			}
			if !strings.Contains(string(page), want) {
				t.Errorf("page has no %q caption", want)
			}
			if strings.Contains(string(page), tc.empty) {
				t.Errorf("page still says %q, the empty-scope reading", tc.empty)
			}
		})
	}
}
