package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// callTool invokes a tool through the protocol, the way a client would.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return result
}

// toolText is a result's text content, for asserting on an error sentence.
func toolText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// decodeStructured unmarshals a result's structured content into out.
func decodeStructured(t *testing.T, result *mcp.CallToolResult, out any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool failed: %s", toolText(result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
}

func TestMarkToolPinsAnExistingResourceWithTheLiveContext(t *testing.T) {
	kube := &recordingKubectl{output: "deployment.apps/api\n"}
	deps := mcpTestDeps(t, kube)
	session := connectMCP(t, deps)

	var out struct {
		Context string  `json:"context"`
		Mark    mcpMark `json:"mark"`
	}
	decodeStructured(t, callTool(t, session, "mark", map[string]any{
		"name": "culprit", "target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)

	marks, _ := deps.State.Marks()
	mark, ok := marks["culprit"]
	if !ok || mark.Kind != kinds.Deployment || mark.Name != "api" || mark.Namespace != "prod" {
		t.Fatalf("marks = %+v", marks)
	}
	if mark.Context != kube.CurrentContext() || out.Context != kube.CurrentContext() {
		t.Errorf("mark context %q / output context %q, want %q", mark.Context, out.Context, kube.CurrentContext())
	}
	want := []string{"get", "Deployment", "api", "-n", "prod", "-o", "name"}
	if len(kube.runs) != 1 || strings.Join(kube.runs[0], " ") != strings.Join(want, " ") {
		t.Errorf("kubectl runs = %v, want one existence check %v", kube.runs, want)
	}
}

// Marks belong to the user. An agent may add one, never move one.
func TestMarkToolRefusesToOverwriteAMark(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{output: "pod/other\n"})
	original := state.Mark{Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"}}
	if err := deps.State.SaveMark("api", original); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "api", "target": map[string]any{"kind": "pods", "name": "other", "namespace": "prod"},
	})
	if !result.IsError || !strings.Contains(toolText(result), "@api already marks") {
		t.Fatalf("result = %s, want an overwrite refusal", toolText(result))
	}
	marks, _ := deps.State.Marks()
	if marks["api"].Name != "api-7d8f" {
		t.Errorf("mark moved to %+v", marks["api"])
	}
}

// A mark that points at nothing would resolve later into a NotFound the user
// never caused; the resource has to exist when it is marked.
func TestMarkToolRefusesAResourceThatDoesNotExist(t *testing.T) {
	kube := &recordingKubectl{err: errors.New(`Error from server (NotFound): deployments.apps "nope" not found`)}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "x", "target": map[string]any{"kind": "deploy", "name": "nope"},
	})
	if !result.IsError {
		t.Fatal("marked a resource kubectl could not find")
	}
	if marks, _ := deps.State.Marks(); len(marks) != 0 {
		t.Errorf("marks = %+v, want none stored", marks)
	}
}

func TestMarkToolValidatesTheName(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{output: "pod/x\n"})
	result := callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "3", "target": map[string]any{"kind": "pods", "name": "x"},
	})
	if !result.IsError || !strings.Contains(toolText(result), "is a number") {
		t.Fatalf("result = %s, want validMarkName's refusal", toolText(result))
	}
}

// Marking must not disturb the listing the user's indexes resolve against.
func TestMarkToolLeavesTheHistoryStackAlone(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{output: "pod/x\n"})
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"web-1"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatal(err)
	}
	callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "x", "target": map[string]any{"kind": "pods", "name": "x", "namespace": "prod"},
	})
	name, namespace, _, err := deps.State.Fields(1)
	if err != nil || name != "web-1" || namespace != "prod" {
		t.Errorf("index 1 = %s/%s, %v; want web-1/prod", name, namespace, err)
	}
}

func TestListMarksToolReturnsMarksSortedByName(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	for _, name := range []string{"web", "api"} {
		if err := deps.State.SaveMark(name, state.Mark{
			Resource: state.Resource{Name: name + "-1", Kind: kinds.Pod, Namespace: "prod"}, Context: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Context string    `json:"context"`
		Marks   []mcpMark `json:"marks"`
	}
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_marks", map[string]any{}), &out)
	if len(out.Marks) != 2 || out.Marks[0].Name != "api" || out.Marks[1].Name != "web" {
		t.Fatalf("marks = %+v, want api then web", out.Marks)
	}
	if out.Marks[0].Resource != "api-1" || out.Marks[0].Kind != "Pod" || out.Context != "test" {
		t.Errorf("first = %+v, context %q", out.Marks[0], out.Context)
	}
}

func TestListResourcesParsesKubectlsTable(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput, namespace: "prod"}
	deps := mcpTestDeps(t, kube)
	var out listOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources", map[string]any{"kind": "pods"}), &out)
	if strings.Join(kube.runs[0], " ") != "get pods -n prod" {
		t.Errorf("kubectl args = %v", kube.runs[0])
	}
	if out.Total != 2 || len(out.Resources) != 2 || out.Resources[0].Name != "nginx-abc-xyz" ||
		out.Resources[0].Kind != "Pod" || out.Resources[0].Namespace != "prod" {
		t.Errorf("out = %+v", out)
	}
}

func TestListResourcesAcrossNamespacesKeepsEachRowsNamespace(t *testing.T) {
	output := "NAMESPACE   NAME   READY   STATUS    RESTARTS   AGE\n" +
		"a           web    1/1     Running   0          1d\n" +
		"b           web    1/1     Running   0          1d\n"
	kube := &recordingKubectl{output: output}
	var out listOutput
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "list_resources",
		map[string]any{"kind": "pods", "allNamespaces": true}), &out)
	if strings.Join(kube.runs[0], " ") != "get pods -A" {
		t.Errorf("kubectl args = %v", kube.runs[0])
	}
	if len(out.Resources) != 2 || out.Resources[0].Namespace != "a" || out.Resources[1].Namespace != "b" {
		t.Errorf("resources = %+v", out.Resources)
	}
}

func TestListResourcesCapsRowsAndSaysHowManyThereWere(t *testing.T) {
	var table strings.Builder
	table.WriteString("NAME   AGE\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&table, "cm-%d   1d\n", i)
	}
	var out listOutput
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, &recordingKubectl{output: table.String()})),
		"list_resources", map[string]any{"kind": "cm", "limit": 2}), &out)
	if out.Total != 5 || len(out.Resources) != 2 {
		t.Errorf("total %d, %d returned; want 5 and 2", out.Total, len(out.Resources))
	}
}

// Nothing found is an empty listing, not an error: kubectl prints nothing on
// stdout and says "No resources found" on stderr.
func TestListResourcesTreatsNoOutputAsNoResources(t *testing.T) {
	var out listOutput
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, &recordingKubectl{})),
		"list_resources", map[string]any{"kind": "pods"}), &out)
	if out.Total != 0 || out.Resources == nil {
		t.Errorf("out = %+v, want an empty, non-null list", out)
	}
}

func TestListResourcesRefusesContradictoryScopes(t *testing.T) {
	session := connectMCP(t, mcpTestDeps(t, &recordingKubectl{}))
	for name, args := range map[string]map[string]any{
		"ns and all":    {"kind": "pods", "namespace": "a", "allNamespaces": true},
		"ns on nodes":   {"kind": "nodes", "namespace": "a"},
		"several kinds": {"kind": "pods,svc"},
		"all":           {"kind": "all"},
	} {
		if result := callTool(t, session, "list_resources", args); !result.IsError {
			t.Errorf("%s: listed, want a refusal", name)
		}
	}
}

func TestListResourcesSavesNoListing(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{output: podsOutput})
	callTool(t, connectMCP(t, deps), "list_resources", map[string]any{"kind": "pods"})
	if _, err := deps.State.Load(); !errors.Is(err, state.ErrNoState) {
		t.Errorf("Load = %v, want ErrNoState — the server saved a listing", err)
	}
}

// A Deployment wanting two replicas with none ready is the cheapest critical
// verdict to build against a fake API server.
func mcpDiagDeps(t *testing.T, kube *recordingKubectl, objects ...runtime.Object) mcpDeps {
	t.Helper()
	deps := mcpTestDeps(t, kube)
	client := fake.NewSimpleClientset(objects...)
	deps.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	return deps
}

func brokenDeployment(name, namespace string) *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(name)},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}

func healthyDeployment(name, namespace string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(name)},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1},
	}
}

type diagnoseResult struct {
	Context   string `json:"context"`
	Diagnosis struct {
		Checked   int `json:"checked"`
		Healthy   int `json:"healthy"`
		Resources []struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Index   int    `json:"index"`
			Mark    string `json:"mark"`
			Verdict string `json:"verdict"`
		} `json:"resources"`
	} `json:"diagnosis"`
}

func TestDiagnoseOneTarget(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{}, brokenDeployment("api", "prod"))
	var out diagnoseResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "diagnose", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)
	if len(out.Diagnosis.Resources) != 1 || out.Diagnosis.Resources[0].Verdict != "critical" {
		t.Fatalf("diagnosis = %+v", out.Diagnosis)
	}
	if out.Diagnosis.Resources[0].Index != 0 {
		t.Errorf("index = %d, want none", out.Diagnosis.Resources[0].Index)
	}
}

func TestDiagnoseNamesTheMarkItWasGiven(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{}, brokenDeployment("api", "prod"))
	if err := deps.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api", Kind: kinds.Deployment, Namespace: "prod"},
	}); err != nil {
		t.Fatal(err)
	}
	var out diagnoseResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "diagnose",
		map[string]any{"target": map[string]any{"mark": "@api"}}), &out)
	if out.Diagnosis.Resources[0].Mark != "api" {
		t.Errorf("mark = %q, want api", out.Diagnosis.Resources[0].Mark)
	}
}

// A sweep returns only what is wrong unless asked for everything, carries no
// indexes, and — the reason it exists as its own test — leaves the listing
// the user's terminal indexes resolve against exactly where it was.
func TestDiagnoseSweepIsUnhealthyOnlyUnindexedAndSavesNothing(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		brokenDeployment("api", "prod"), healthyDeployment("web", "prod"))
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"user-pod"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, deps)

	var out diagnoseResult
	decodeStructured(t, callTool(t, session, "diagnose", map[string]any{}), &out)
	if out.Diagnosis.Checked != 2 || out.Diagnosis.Healthy != 1 {
		t.Errorf("checked %d healthy %d, want 2 and 1", out.Diagnosis.Checked, out.Diagnosis.Healthy)
	}
	if len(out.Diagnosis.Resources) != 1 || out.Diagnosis.Resources[0].Name != "api" {
		t.Errorf("resources = %+v, want only api", out.Diagnosis.Resources)
	}
	for _, resource := range out.Diagnosis.Resources {
		if resource.Index != 0 {
			t.Errorf("%s carries index %d", resource.Name, resource.Index)
		}
	}

	var full diagnoseResult
	decodeStructured(t, callTool(t, session, "diagnose", map[string]any{"full": true}), &full)
	if len(full.Diagnosis.Resources) != 2 {
		t.Errorf("full sweep returned %d resources, want 2", len(full.Diagnosis.Resources))
	}

	name, _, _, err := deps.State.Fields(1)
	if err != nil || name != "user-pod" {
		t.Errorf("index 1 = %q, %v; the sweep replaced the user's listing", name, err)
	}
}

func TestDiagnoseRefusesContradictoryArguments(t *testing.T) {
	session := connectMCP(t, mcpDiagDeps(t, &recordingKubectl{}))
	target := map[string]any{"kind": "deploy", "name": "api"}
	for name, args := range map[string]map[string]any{
		"target and namespace": {"target": target, "namespace": "prod"},
		"target and all":       {"target": target, "allNamespaces": true},
		"target and full":      {"target": target, "full": true},
		"namespace and all":    {"namespace": "prod", "allNamespaces": true},
		"bad since":            {"since": "soon"},
	} {
		if result := callTool(t, session, "diagnose", args); !result.IsError {
			t.Errorf("%s: diagnosed, want a refusal", name)
		}
	}
}

type treeResult struct {
	Context string `json:"context"`
	Tree    struct {
		Roots []jsonTreeNode `json:"roots"`
	} `json:"tree"`
}

func mcpTreeDeps(t *testing.T) mcpDeps {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	client := treeFixture().Client
	deps.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	return deps
}

func TestTreeToolGraphsATargetWithoutIndexes(t *testing.T) {
	deps := mcpTreeDeps(t)
	var out treeResult
	decodeStructured(t, callTool(t, connectMCP(t, deps), "tree", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"},
	}), &out)
	if len(out.Tree.Roots) != 1 || out.Tree.Roots[0].Name != "web" {
		t.Fatalf("roots = %+v", out.Tree.Roots)
	}
	var walk func(node jsonTreeNode)
	walk = func(node jsonTreeNode) {
		if node.Index != 0 {
			t.Errorf("%s carries index %d", node.Name, node.Index)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(out.Tree.Roots[0])
	if _, err := deps.State.Load(); !errors.Is(err, state.ErrNoState) {
		t.Errorf("Load = %v; the tree saved a listing", err)
	}
}

func TestTreeToolGraphsTheCurrentNamespaceByDefault(t *testing.T) {
	var out treeResult
	decodeStructured(t, callTool(t, connectMCP(t, mcpTreeDeps(t)), "tree", map[string]any{}), &out)
	if len(out.Tree.Roots) != 1 {
		t.Fatalf("roots = %+v, want the prod namespace forest", out.Tree.Roots)
	}
}

// The same refusal list_resources and diagnose make for -n beside -A,
// spelled the same way for tree — and, on top of it, tree's own rule that a
// target already names its namespace.
func TestTreeToolRefusesContradictoryScopes(t *testing.T) {
	session := connectMCP(t, mcpTreeDeps(t))
	target := map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"}
	for name, args := range map[string]map[string]any{
		"target and namespace": {"target": target, "namespace": "prod"},
		"target and all":       {"target": target, "allNamespaces": true},
		"namespace and all":    {"namespace": "prod", "allNamespaces": true},
	} {
		if result := callTool(t, session, "tree", args); !result.IsError {
			t.Errorf("%s: graphed, want a refusal", name)
		}
	}
}
