package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jzills/kx/internal/kinds"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// newFakeEvent builds one namespaced Event involving the named object, dated
// at `at`. Named "newFakeEvent" rather than "event" or "mcpEvent" — the
// latter is the tool's own output row type, declared in mcp_evidence.go.
func newFakeEvent(name, kind, reason string, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name + "." + reason, Namespace: "prod"},
		InvolvedObject: corev1.ObjectReference{Name: name, Kind: kind},
		Type:           "Warning",
		Reason:         reason,
		Message:        reason + " happened",
		LastTimestamp:  metav1.NewTime(at),
	}
}

func TestEventsToolReturnsOnlyTheTargetsEventsNewestFirst(t *testing.T) {
	now := time.Now()
	older := newFakeEvent("api", "Deployment", "ScalingReplicaSet", now.Add(-time.Hour))
	newer := newFakeEvent("api", "Deployment", "FailedScheduling", now.Add(-time.Minute))
	other := newFakeEvent("web", "Deployment", "Killing", now)
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		brokenDeployment("api", "prod"), older, newer, other)

	var out eventsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "events", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)

	if out.Total != 2 || len(out.Events) != 2 {
		t.Fatalf("events = %+v, want 2 of api's own", out)
	}
	if out.Events[0].Reason != "FailedScheduling" || out.Events[1].Reason != "ScalingReplicaSet" {
		t.Errorf("order = %+v, want newest first", out.Events)
	}
	if _, err := time.Parse(time.RFC3339, out.Events[0].At); err != nil {
		t.Errorf("at = %q, not RFC 3339: %v", out.Events[0].At, err)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "index") {
		t.Errorf("output carries an index: %s", raw)
	}
}

func TestEventsToolSinceDropsOlderEvents(t *testing.T) {
	now := time.Now()
	old := newFakeEvent("api", "Deployment", "Old", now.Add(-2*time.Hour))
	recent := newFakeEvent("api", "Deployment", "New", now.Add(-time.Minute))
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"},
		brokenDeployment("api", "prod"), old, recent)

	var out eventsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "events", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
		"since":  "30m",
	}), &out)

	if out.Total != 1 || len(out.Events) != 1 || out.Events[0].Reason != "New" {
		t.Fatalf("out = %+v, want only the recent event", out)
	}
	if out.Window != "30m" {
		t.Errorf("window = %q, want 30m", out.Window)
	}
}

func TestEventsToolLimitCapsAndSetsTruncated(t *testing.T) {
	now := time.Now()
	objects := []runtime.Object{brokenDeployment("api", "prod")}
	for i := 0; i < 5; i++ {
		objects = append(objects, newFakeEvent("api", "Deployment", fmt.Sprintf("E%d", i),
			now.Add(-time.Duration(i)*time.Minute)))
	}
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"}, objects...)

	var out eventsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "events", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
		"limit":  2,
	}), &out)

	if out.Total != 5 || len(out.Events) != 2 || out.Truncated != 3 {
		t.Fatalf("out = %+v, want total 5, 2 kept, truncated 3", out)
	}
	if out.Events[0].Reason != "E0" || out.Events[1].Reason != "E1" {
		t.Errorf("kept = %+v, want the two newest (E0, E1)", out.Events)
	}
}

// No matching events and a probe that says the resource is gone: the CLI's
// own StaleResourceError.Error() names an index for a Ref this package never
// has, so the tool must translate it into a sentence that doesn't.
func TestEventsToolResourceGoneIsATranslatedError(t *testing.T) {
	kube := &recordingKubectl{namespace: "prod", probeCode: 1}
	deps := mcpDiagDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "events", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	})
	if !result.IsError {
		t.Fatal("succeeded, want an error for a vanished resource")
	}
	text := toolText(result)
	if strings.Contains(strings.ToLower(text), "index") {
		t.Errorf("error mentions an index: %q", text)
	}
	if want := "Deployment/api no longer exists in prod."; text != want {
		t.Errorf("error = %q, want %q", text, want)
	}
}

func TestEventsToolNoEventsButResourceExistsIsAnEmptyList(t *testing.T) {
	deps := mcpDiagDeps(t, &recordingKubectl{namespace: "prod"}, brokenDeployment("api", "prod"))
	var out eventsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "events", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)
	if out.Total != 0 || out.Events == nil || len(out.Events) != 0 {
		t.Errorf("out = %+v, want an empty, non-null list", out)
	}
}

func TestLogsToolOnAPodBuildsExactArgv(t *testing.T) {
	kube := &recordingKubectl{output: "log line\n"}
	deps := mcpTestDeps(t, kube)
	var out logsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "logs", map[string]any{
		"target":    map[string]any{"kind": "pods", "name": "api-7d8f", "namespace": "prod"},
		"container": "app",
		"since":     "30m",
		"tail":      50,
		"previous":  true,
	}), &out)

	want := []string{"logs", "api-7d8f", "-n", "prod", "--tail=50", "--since=30m0s", "-c", "app", "--previous"}
	if len(kube.runs) != 1 || strings.Join(kube.runs[0], " ") != strings.Join(want, " ") {
		t.Fatalf("kubectl args = %v, want %v", kube.runs, want)
	}
	if out.Logs != "log line\n" || out.Lines != 1 || out.Truncated {
		t.Errorf("out = %+v", out)
	}
}

func TestLogsToolOnADeploymentReadsSelectorThenAggregates(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{
		`{"spec":{"selector":{"matchLabels":{"app":"web"}}}}`,
		"[web-1] hello\n",
	}}
	deps := mcpTestDeps(t, kube)
	var out logsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "logs", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"},
	}), &out)

	if len(kube.runs) != 2 {
		t.Fatalf("kubectl runs = %v, want 2", kube.runs)
	}
	wantSelector := []string{"get", "Deployment", "web", "-n", "prod", "-o", "json"}
	if strings.Join(kube.runs[0], " ") != strings.Join(wantSelector, " ") {
		t.Errorf("first run = %v, want %v", kube.runs[0], wantSelector)
	}
	wantLogs := []string{"logs", "-l", "app=web", "--prefix=true", "-n", "prod", "--tail=200"}
	if strings.Join(kube.runs[1], " ") != strings.Join(wantLogs, " ") {
		t.Errorf("second run = %v, want %v", kube.runs[1], wantLogs)
	}
	if out.Logs != "[web-1] hello\n" {
		t.Errorf("logs = %q", out.Logs)
	}
}

func TestLogsToolRefusesContainerWithAnAggregateKind(t *testing.T) {
	kube := &recordingKubectl{}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "logs", map[string]any{
		"target":    map[string]any{"kind": "deploy", "name": "web", "namespace": "prod"},
		"container": "app",
	})
	if !result.IsError || !strings.Contains(toolText(result), "applies to a Pod target") {
		t.Fatalf("result = %s, want the container refusal", toolText(result))
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %v before the refusal", kube.runs)
	}
}

func TestLogsToolRefusesAnUnsupportedKind(t *testing.T) {
	kube := &recordingKubectl{}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "logs", map[string]any{
		"target": map[string]any{"kind": "configmaps", "name": "cfg", "namespace": "prod"},
	})
	want := unsupportedKindError("logs", kinds.ConfigMap, logKinds).Error()
	if !result.IsError || toolText(result) != want {
		t.Errorf("result = %q, want %q", toolText(result), want)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %v for an unsupported kind", kube.runs)
	}
}

func TestLogsToolRefusesFlagShapedContainerNames(t *testing.T) {
	for _, container := range []string{"-c", "--foo"} {
		t.Run(container, func(t *testing.T) {
			kube := &recordingKubectl{}
			deps := mcpTestDeps(t, kube)
			result := callTool(t, connectMCP(t, deps), "logs", map[string]any{
				"target":    map[string]any{"kind": "pods", "name": "api", "namespace": "prod"},
				"container": container,
			})
			if !result.IsError {
				t.Errorf("container %q accepted, want a refusal", container)
			}
			if len(kube.runs) != 0 {
				t.Errorf("container %q: kubectl ran %v before the refusal", container, kube.runs)
			}
		})
	}
}

func TestLogsToolBoundsOutputAtTheLineBoundary(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "line %05d filler filler filler filler\n", i)
	}
	big := b.String()
	kube := &recordingKubectl{output: big}
	deps := mcpTestDeps(t, kube)
	var out logsOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "logs", map[string]any{
		"target": map[string]any{"kind": "pods", "name": "api", "namespace": "prod"},
	}), &out)

	if !out.Truncated {
		t.Fatalf("truncated = false, want true for %d bytes of output", len(big))
	}
	if len(out.Logs) > 256*1024 {
		t.Errorf("logs is %d bytes, want at most 256 KiB", len(out.Logs))
	}
	if !strings.HasSuffix(big, out.Logs) {
		t.Errorf("kept text is not the tail of the original output")
	}
	cutAt := len(big) - len(out.Logs)
	if cutAt > 0 && big[cutAt-1] != '\n' {
		t.Errorf("cut mid-line at offset %d", cutAt)
	}
	if out.Lines != countLines(out.Logs) {
		t.Errorf("lines = %d, want %d matching the kept text", out.Lines, countLines(out.Logs))
	}
}
