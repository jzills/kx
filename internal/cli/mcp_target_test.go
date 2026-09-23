package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// mcpTestDeps is a server's dependencies against a temp state file and a fake
// kubectl, with no API client — tools that need one set Kubernetes themselves.
func mcpTestDeps(t *testing.T, kube kubectl.Service) mcpDeps {
	t.Helper()
	return mcpDeps{
		Kubectl: kube,
		State: &state.Service{
			MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json"),
			Context: kube.CurrentContext,
		},
		Config: config.Default(),
	}
}

func TestResolveTargetNormalizesKubectlSpellings(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	got, err := deps.resolveTarget(mcpTarget{Kind: "deploy", Name: "api"})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.Kind != kinds.Deployment || got.Name != "api" || got.Namespace != "prod" {
		t.Errorf("got %+v, want Deployment/api in the current namespace prod", got)
	}
}

func TestResolveTargetRefusesANamespaceOnAClusterScopedKind(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	_, err := deps.resolveTarget(mcpTarget{Kind: "nodes", Name: "n1", Namespace: "prod"})
	if err == nil || !strings.Contains(err.Error(), "cluster-scoped") {
		t.Fatalf("err = %v, want a cluster-scoped refusal", err)
	}
	got, err := deps.resolveTarget(mcpTarget{Kind: "nodes", Name: "n1"})
	if err != nil || got.Namespace != "" {
		t.Errorf("got %+v, %v; want Node/n1 with no namespace", got, err)
	}
}

func TestResolveTargetReadsAMark(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	if err := deps.State.SaveMark("api", state.Mark{
		Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	for _, spelling := range []string{"api", "@api"} {
		got, err := deps.resolveTarget(mcpTarget{Mark: spelling})
		if err != nil {
			t.Fatalf("%s: %v", spelling, err)
		}
		if got.Name != "api-7d8f" || got.Kind != kinds.Pod || got.Namespace != "prod" || got.Mark != "api" {
			t.Errorf("%s: got %+v", spelling, got)
		}
	}
}

func TestResolveTargetRefusesAmbiguousOrEmptyTargets(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	for name, target := range map[string]mcpTarget{
		"mark and kind": {Mark: "api", Kind: "pods", Name: "x"},
		"nothing":       {},
		"no name":       {Kind: "pods"},
		"two kinds":     {Kind: "pods,svc", Name: "x"},
		"kind/name":     {Kind: "pod/x", Name: "x"},
	} {
		if _, err := deps.resolveTarget(target); err == nil {
			t.Errorf("%s: resolved, want a refusal", name)
		}
	}
}

// The CLI memoises the context for the life of the process. The server must
// not: a user who switches context mid-session has to see the next call
// answer from the new cluster.
func TestLiveKubectlRereadsTheContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	write := func(current string) {
		body := "apiVersion: v1\nkind: Config\ncurrent-context: " + current + "\n" +
			"contexts:\n- name: a\n  context: {cluster: c, user: u}\n" +
			"- name: b\n  context: {cluster: c, user: u}\n" +
			"clusters:\n- name: c\n  cluster: {server: https://127.0.0.1:1}\n" +
			"users:\n- name: u\n  user: {}\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KUBECONFIG", path)
	write("a")
	deps := liveMCPDeps(Services{State: &state.Service{}, Config: config.Default()})
	if got := deps.Kubectl.CurrentContext(); got != "a" {
		t.Fatalf("context = %q, want a", got)
	}
	write("b")
	if got := deps.Kubectl.CurrentContext(); got != "b" {
		t.Errorf("context = %q after switching, want b — the server is caching", got)
	}
}
