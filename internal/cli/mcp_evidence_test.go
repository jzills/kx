package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// A bad since is refused naming the tool's own field, not the CLI flag the
// agent never saw — the same sentence from every tool that takes one.
func TestMCPSinceRefusalsNameTheField(t *testing.T) {
	target := map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}
	session := connectMCP(t, mcpDiagDeps(t, &recordingKubectl{namespace: "prod"}, brokenDeployment("api", "prod")))
	for tool, args := range map[string]map[string]any{
		"events":   {"target": target, "since": "soon"},
		"logs":     {"target": target, "since": "soon"},
		"diagnose": {"since": "soon"},
	} {
		result := callTool(t, session, tool, args)
		text := toolText(result)
		if !result.IsError || !strings.HasPrefix(text, "'since': invalid duration \"soon\"") {
			t.Errorf("%s: result = %q, want a refusal naming 'since'", tool, text)
		}
		if strings.Contains(text, "--since") {
			t.Errorf("%s: result = %q names the CLI flag", tool, text)
		}
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

// A single line longer than the whole cap has no newline to cut at, so the
// naive "last maxLogBytes bytes" slice can start in the middle of a
// multi-byte rune. boundLogBytes must advance to the next rune boundary
// instead, so the kept text is always valid UTF-8.
func TestBoundLogBytesAdvancesToARuneBoundaryWhenThereIsNoNewline(t *testing.T) {
	// "世" is 3 bytes in UTF-8; maxLogBytes (256 KiB) is not a multiple of 3,
	// so the naive cut point lands inside a rune's bytes rather than at its
	// start.
	text := strings.Repeat("世", 100000) // 300,000 bytes, one line, no newline
	got, truncated := boundLogBytes(text)

	if !truncated {
		t.Fatalf("truncated = false, want true for %d bytes with no newline", len(text))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("boundLogBytes returned invalid UTF-8: %q", got)
	}
	if len(got) > maxLogBytes {
		t.Errorf("kept %d bytes, want at most %d", len(got), maxLogBytes)
	}
	if !strings.HasSuffix(text, got) {
		t.Errorf("kept text is not the tail of the original output")
	}
}

func TestTopToolPodsReturnsRowsWithNoIndexAndLiveContext(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{topPodsFixture, podsJSON}}
	deps := mcpTestDeps(t, kube)
	var out topOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "top", map[string]any{}), &out)

	if out.Context != kube.CurrentContext() {
		t.Errorf("context = %q, want %q", out.Context, kube.CurrentContext())
	}
	if out.Top.Resource != "pods" {
		t.Errorf("resource = %q, want pods", out.Top.Resource)
	}
	if len(out.Top.Rows) != 2 {
		t.Fatalf("rows = %+v, want 2", out.Top.Rows)
	}
	for _, row := range out.Top.Rows {
		if row.Index != 0 {
			t.Errorf("row %+v carries an index", row)
		}
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), `"index"`) {
		t.Errorf("output carries an index: %s", raw)
	}
}

func TestTopToolNodesRunsTopNodesNotThePodsPath(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{nodesOutput}}
	deps := mcpTestDeps(t, kube)
	var out topOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "top", map[string]any{"nodes": true}), &out)

	if len(kube.runs) != 1 || kube.runs[0][0] != "top" || kube.runs[0][1] != "nodes" {
		t.Fatalf("kubectl runs = %v, want a single 'top nodes'", kube.runs)
	}
	if out.Top.Resource != "nodes" {
		t.Errorf("resource = %q, want nodes", out.Top.Resource)
	}
	if len(out.Top.Rows) != 2 {
		t.Fatalf("rows = %+v, want 2", out.Top.Rows)
	}
}

func TestTopToolMetricsServerUnavailableGivesTheSentence(t *testing.T) {
	kube := &recordingKubectl{probeCode: 1}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "top", map[string]any{})
	if !result.IsError || !strings.Contains(toolText(result), "metrics-server is not available") {
		t.Fatalf("result = %q, want the metrics-server sentence", toolText(result))
	}
}

// TopCommand.Execute normally saves a listing to spend the indexes it just
// printed. Nothing here was printed, so the server must build TopCommand with
// a discarding writer rather than the real state.Service.
func TestTopToolSavesNoListing(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{topPodsFixture, podsJSON}}
	deps := mcpTestDeps(t, kube)
	callTool(t, connectMCP(t, deps), "top", map[string]any{})
	if _, err := deps.State.Load(); !errors.Is(err, state.ErrNoState) {
		t.Errorf("Load = %v, want ErrNoState — the server saved a listing", err)
	}
}

func TestTopToolLimitTruncates(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{topPodsFixture, podsJSON}}
	deps := mcpTestDeps(t, kube)
	var out topOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "top", map[string]any{"limit": 1}), &out)

	if len(out.Top.Rows) != 1 {
		t.Fatalf("rows = %+v, want 1", out.Top.Rows)
	}
	if out.Top.Truncated != 1 {
		t.Errorf("truncated = %d, want 1", out.Top.Truncated)
	}
}

func TestTopToolNodesWithNamespaceGetsTheClusterScopedSentence(t *testing.T) {
	kube := &recordingKubectl{}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "top", map[string]any{"nodes": true, "namespace": "prod"})
	want := clusterScopedScopeError("namespace", "nodes").Error()
	if !result.IsError || toolText(result) != want {
		t.Errorf("result = %q, want %q", toolText(result), want)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %v before the refusal", kube.runs)
	}
}

func TestGetYamlToolReturnsTheManifest(t *testing.T) {
	kube := &recordingKubectl{output: "apiVersion: v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 2\n"}
	deps := mcpTestDeps(t, kube)
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
	}), &out)

	want := []string{"get", "Deployment", "api", "-n", "prod", "-o", "yaml"}
	if len(kube.runs) != 1 || strings.Join(kube.runs[0], " ") != strings.Join(want, " ") {
		t.Fatalf("kubectl args = %v, want %v", kube.runs, want)
	}
	if out.Kind != "Deployment" || out.Name != "api" || out.Namespace != "prod" {
		t.Errorf("out = %+v", out)
	}
	if out.Redacted {
		t.Errorf("redacted = true for a non-Secret")
	}
	if !strings.Contains(out.YAML, "replicas: 2") {
		t.Errorf("yaml = %q, want it to carry spec.replicas", out.YAML)
	}
}

func TestGetYamlToolFieldsNarrowsIt(t *testing.T) {
	kube := &recordingKubectl{output: "apiVersion: v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  replicas: 2\nstatus:\n  readyReplicas: 1\n"}
	deps := mcpTestDeps(t, kube)
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"},
		"fields": []string{"spec"},
	}), &out)

	if !strings.Contains(out.YAML, "replicas: 2") {
		t.Errorf("yaml = %q, want spec kept", out.YAML)
	}
	if strings.Contains(out.YAML, "readyReplicas") {
		t.Errorf("yaml = %q, want status dropped", out.YAML)
	}
}

const secretManifest = "apiVersion: v1\n" +
	"kind: Secret\n" +
	"metadata:\n" +
	"  name: creds\n" +
	"  annotations:\n" +
	"    kubectl.kubernetes.io/last-applied-configuration: '{\"data\":{\"password\":\"c2VjcmV0\"}}'\n" +
	"data:\n" +
	"  password: c2VjcmV0\n" +
	"stringData:\n" +
	"  token: plaintext-token\n"

func TestGetYamlToolRedactsASecret(t *testing.T) {
	kube := &recordingKubectl{output: secretManifest}
	deps := mcpTestDeps(t, kube)
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "secret", "name": "creds", "namespace": "prod"},
	}), &out)

	if !out.Redacted {
		t.Fatalf("redacted = false, want true for a Secret")
	}
	if strings.Contains(out.YAML, "c2VjcmV0") || strings.Contains(out.YAML, "plaintext-token") {
		t.Errorf("yaml leaks a Secret value: %s", out.YAML)
	}
	if !strings.Contains(out.YAML, "password: <redacted>") || !strings.Contains(out.YAML, "token: <redacted>") {
		t.Errorf("yaml = %q, want the keys kept with redacted values", out.YAML)
	}
	if !strings.Contains(out.YAML, "last-applied-configuration: <redacted>") {
		t.Errorf("yaml = %q, want the last-applied annotation redacted", out.YAML)
	}
}

// A mark that aliases a Secret must be redacted on the kind it resolves to,
// not on whatever the caller happened to spell in the target.
func TestGetYamlToolRedactsASecretThroughAMark(t *testing.T) {
	kube := &recordingKubectl{output: secretManifest}
	deps := mcpTestDeps(t, kube)
	if err := deps.State.SaveMark("db", state.Mark{
		Resource: state.Resource{Name: "creds", Kind: kinds.Secret, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"mark": "db"},
	}), &out)

	if !out.Redacted {
		t.Fatalf("redacted = false, want true for a mark aliasing a Secret")
	}
	if strings.Contains(out.YAML, "c2VjcmV0") {
		t.Errorf("yaml leaks a Secret value: %s", out.YAML)
	}
}

// kubectl resolves each of these to core/v1 Secrets. Every one must come
// back redacted, with no plaintext anywhere in the result.
func TestGetYamlToolRedactsDottedSecretSpellings(t *testing.T) {
	for _, spelling := range []string{"secrets.", "secrets.v1.", "secret.v1.", "Secret.v1."} {
		t.Run(spelling, func(t *testing.T) {
			kube := &recordingKubectl{output: secretManifest}
			deps := mcpTestDeps(t, kube)
			result := callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
				"target": map[string]any{"kind": spelling, "name": "creds", "namespace": "prod"},
			})
			var out yamlOutput
			decodeStructured(t, result, &out)
			if !out.Redacted {
				t.Errorf("redacted = false, want true")
			}
			if out.Kind != string(kinds.Secret) {
				t.Errorf("kind = %q, want the canonical Secret", out.Kind)
			}
			assertNoSecretPlaintext(t, result)
		})
	}
}

// A mark taken on a dotted spelling must store the canonical kind, so a
// later get_yaml by that mark is redacted like any other Secret.
func TestMarkWithADottedSecretKindIsRedactedThroughTheMark(t *testing.T) {
	kube := &recordingKubectl{output: secretManifest}
	deps := mcpTestDeps(t, kube)
	session := connectMCP(t, deps)
	if result := callTool(t, session, "mark", map[string]any{
		"name":   "db",
		"target": map[string]any{"kind": "secrets.", "name": "creds", "namespace": "prod"},
	}); result.IsError {
		t.Fatalf("mark: %s", toolText(result))
	}
	marks, err := deps.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if got := marks["db"].Kind; got != kinds.Secret {
		t.Errorf("stored kind = %q, want the canonical Secret", got)
	}
	result := callTool(t, session, "get_yaml", map[string]any{"target": map[string]any{"mark": "db"}})
	var out yamlOutput
	decodeStructured(t, result, &out)
	if !out.Redacted {
		t.Errorf("redacted = false, want true for a mark taken on secrets.")
	}
	assertNoSecretPlaintext(t, result)
}

// Redaction is decided by what came back as well as by what was asked for:
// a manifest that is a core/v1 Secret is redacted whatever the target's kind
// was spelled as.
func TestGetYamlToolRedactsASecretByContent(t *testing.T) {
	kube := &recordingKubectl{output: secretManifest}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "widgets", "name": "creds", "namespace": "prod"},
	})
	var out yamlOutput
	decodeStructured(t, result, &out)
	if !out.Redacted {
		t.Errorf("redacted = false, want true for a manifest that is a Secret")
	}
	assertNoSecretPlaintext(t, result)
}

// A kind named Secret in some other API group is not a core Secret.
func TestGetYamlToolDoesNotRedactASecretKindInAnotherGroup(t *testing.T) {
	kube := &recordingKubectl{output: "apiVersion: example.com/v1\nkind: Secret\nmetadata:\n  name: x\ndata:\n  key: value\n"}
	deps := mcpTestDeps(t, kube)
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "secrets.example.com", "name": "x", "namespace": "prod"},
	}), &out)
	if out.Redacted || !strings.Contains(out.YAML, "key: value") {
		t.Errorf("out = %+v, want a non-core Secret kind left as-is", out)
	}
}

func assertNoSecretPlaintext(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, plaintext := range []string{"c2VjcmV0", "plaintext-token"} {
		if strings.Contains(string(encoded), plaintext) {
			t.Errorf("result leaks %q: %s", plaintext, encoded)
		}
	}
}

func TestGetYamlToolDoesNotRedactANonSecret(t *testing.T) {
	kube := &recordingKubectl{output: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n  key: value\n"}
	deps := mcpTestDeps(t, kube)
	var out yamlOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "configmaps", "name": "cfg", "namespace": "prod"},
	}), &out)

	if out.Redacted {
		t.Errorf("redacted = true for a ConfigMap")
	}
	if !strings.Contains(out.YAML, "key: value") {
		t.Errorf("yaml = %q, want data kept as-is", out.YAML)
	}
}

func TestGetYamlToolRefusesAnOversizedManifest(t *testing.T) {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "  key%05d: filler-filler-filler-filler-filler\n", i)
	}
	kube := &recordingKubectl{output: b.String()}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
		"target": map[string]any{"kind": "configmaps", "name": "cfg", "namespace": "prod"},
	})
	if !result.IsError || !strings.Contains(toolText(result), "over the 256 KiB limit") {
		t.Fatalf("result = %q, want the size refusal", toolText(result))
	}
	if !strings.Contains(toolText(result), `["spec"]`) {
		t.Errorf("result = %q, want the fields hint", toolText(result))
	}
}

func TestGetYamlToolRefusesInvalidFieldNames(t *testing.T) {
	for _, field := range []string{"meta-data", "spec.replicas", "-oyaml", "spec/replicas"} {
		t.Run(field, func(t *testing.T) {
			kube := &recordingKubectl{output: "apiVersion: v1\nkind: ConfigMap\n"}
			deps := mcpTestDeps(t, kube)
			result := callTool(t, connectMCP(t, deps), "get_yaml", map[string]any{
				"target": map[string]any{"kind": "configmaps", "name": "cfg", "namespace": "prod"},
				"fields": []string{field},
			})
			if !result.IsError {
				t.Errorf("field %q accepted, want a refusal", field)
			}
			if len(kube.runs) != 0 {
				t.Errorf("field %q: kubectl ran %v before the refusal", field, kube.runs)
			}
		})
	}
}

func TestRedactSecret(t *testing.T) {
	tests := map[string]struct {
		in   map[string]any
		want map[string]any
	}{
		"absent data": {
			in:   map[string]any{"kind": "Secret"},
			want: map[string]any{"kind": "Secret"},
		},
		"nil data": {
			in:   map[string]any{"kind": "Secret", "data": nil, "stringData": nil},
			want: map[string]any{"kind": "Secret", "data": nil, "stringData": nil},
		},
		"empty maps": {
			in:   map[string]any{"data": map[string]any{}, "stringData": map[string]any{}},
			want: map[string]any{"data": map[string]any{}, "stringData": map[string]any{}},
		},
		"no annotations": {
			in:   map[string]any{"metadata": map[string]any{"name": "creds"}},
			want: map[string]any{"metadata": map[string]any{"name": "creds"}},
		},
		"data and stringData redacted, keys kept": {
			in: map[string]any{
				"data":       map[string]any{"password": "c2VjcmV0"},
				"stringData": map[string]any{"token": "plaintext"},
			},
			want: map[string]any{
				"data":       map[string]any{"password": "<redacted>"},
				"stringData": map[string]any{"token": "<redacted>"},
			},
		},
		"last-applied annotation redacted, others kept": {
			in: map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"c2VjcmV0"}}`,
						"other": "kept",
					},
				},
			},
			want: map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"kubectl.kubernetes.io/last-applied-configuration": "<redacted>",
						"other": "kept",
					},
				},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := redactSecret(tc.in)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("redactSecret(%v) = %s, want %s", tc.in, gotJSON, wantJSON)
			}
		})
	}
}

// A non-map document (nil, a scalar, a malformed manifest) has nothing
// shaped like a Secret to redact, so redactSecret must return it unchanged
// rather than panic.
func TestRedactSecretNonMapDocument(t *testing.T) {
	for name, doc := range map[string]any{"nil": nil, "string": "not a manifest", "slice": []any{1, 2}} {
		t.Run(name, func(t *testing.T) {
			got := redactSecret(doc)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(doc)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("redactSecret(%v) = %s, want unchanged %s", doc, gotJSON, wantJSON)
			}
		})
	}
}
