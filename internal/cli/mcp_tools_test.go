package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
