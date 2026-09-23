package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		mu:     &sync.Mutex{},
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
	if err == nil || !strings.Contains(err.Error(), "outside any namespace") {
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

// Every target field reaches kubectl's argv. A name of "-lapp=api" turns the
// existence check into a selector query that succeeds — and the mark it
// stores would later hand `kx delete @x` a selector-wide delete; a name of
// "--server=…" sends the kubeconfig's credentials somewhere else. So a kind,
// name or namespace that is not shaped like one is refused before anything
// runs, and nothing is stored.
func TestMarkToolRefusesFlagShapedTargets(t *testing.T) {
	for label, target := range map[string]map[string]any{
		"selector name":       {"kind": "pods", "name": "-lapp=api", "namespace": "prod"},
		"server name":         {"kind": "pods", "name": "--server=https://evil"},
		"context name":        {"kind": "pods", "name": "--context=x"},
		"kubeconfig name":     {"kind": "pods", "name": "--kubeconfig=/tmp/x"},
		"output name":         {"kind": "pods", "name": "-oyaml"},
		"name with a space":   {"kind": "pods", "name": "api web"},
		"uppercase name":      {"kind": "pods", "name": "API"},
		"name ending in dash": {"kind": "pods", "name": "api-"},
		"overlong name":       {"kind": "pods", "name": strings.Repeat("a", 254)},
		"flag kind":           {"kind": "--context=x", "name": "api"},
		"short flag kind":     {"kind": "-oyaml", "name": "api"},
		"two kinds":           {"kind": "pods,svc", "name": "api"},
		"kind/name":           {"kind": "pod/x", "name": "api"},
		"all":                 {"kind": "all", "name": "api"},
		"flag namespace":      {"kind": "pods", "name": "api", "namespace": "-A"},
		"kubeconfig ns":       {"kind": "pods", "name": "api", "namespace": "--kubeconfig=/tmp/x"},
		"uppercase ns":        {"kind": "pods", "name": "api", "namespace": "Prod"},
		"leading colon name":  {"kind": "clusterroles", "name": ":foo"},
		"colon namespace":     {"kind": "pods", "name": "api", "namespace": "system:prod"},
		"dotted namespace":    {"kind": "pods", "name": "api", "namespace": "prod.eu"},
	} {
		t.Run(label, func(t *testing.T) {
			kube := &recordingKubectl{output: "pod/api\n"}
			deps := mcpTestDeps(t, kube)
			result := callTool(t, connectMCP(t, deps), "mark", map[string]any{"name": "x", "target": target})
			if !result.IsError {
				t.Fatalf("marked %v, want a refusal", target)
			}
			if len(kube.runs) != 0 {
				t.Errorf("kubectl ran %v before the target was refused", kube.runs)
			}
			if marks, _ := deps.State.Marks(); len(marks) != 0 {
				t.Errorf("marks = %+v, want none stored", marks)
			}
		})
	}
}

// diagnose and tree take the same target, through the same resolveTarget.
func TestResolveTargetRefusesFlagShapedFields(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	for _, target := range []mcpTarget{
		{Kind: "pods", Name: "-lapp=api"},
		{Kind: "pods", Name: "--server=https://evil"},
		{Kind: "-oyaml", Name: "api"},
		{Kind: "pods", Name: "api", Namespace: "-A"},
	} {
		if _, err := deps.resolveTarget(target); err == nil {
			t.Errorf("%+v resolved, want a refusal", target)
		}
	}
	// Real spellings still resolve: a CRD's dotted plural, a dotted name.
	for _, target := range []mcpTarget{
		{Kind: "certificates.cert-manager.io", Name: "web-tls"},
		{Kind: "Deployment", Name: "api.v2", Namespace: "kube-system"},
	} {
		if _, err := deps.resolveTarget(target); err != nil {
			t.Errorf("%+v: %v, want it resolved", target, err)
		}
	}
}

func TestListResourcesRefusesFlagShapedArguments(t *testing.T) {
	for label, args := range map[string]map[string]any{
		"flag kind":       {"kind": "-oyaml"},
		"context kind":    {"kind": "--context=x"},
		"server kind":     {"kind": "--server=https://evil"},
		"flag namespace":  {"kind": "pods", "namespace": "-A"},
		"kubeconfig ns":   {"kind": "pods", "namespace": "--kubeconfig=/tmp/x"},
		"namespace space": {"kind": "pods", "namespace": "a b"},
	} {
		t.Run(label, func(t *testing.T) {
			kube := &recordingKubectl{output: podsOutput}
			result := callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "list_resources", args)
			if !result.IsError {
				t.Fatalf("listed %v, want a refusal", args)
			}
			if len(kube.runs) != 0 {
				t.Errorf("kubectl ran %v before the arguments were refused", kube.runs)
			}
		})
	}
}

// RBAC's own objects are named with colons — system:aggregate-to-admin,
// system:controller:… — and a colon is inert in argv, so a name may carry
// one anywhere but first. Only a leading '-' is dangerous.
func TestMarkToolAcceptsColonedRBACNames(t *testing.T) {
	kube := &recordingKubectl{output: "clusterrole.rbac.authorization.k8s.io/system:aggregate-to-admin\n"}
	deps := mcpTestDeps(t, kube)
	result := callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "admin", "target": map[string]any{"kind": "clusterroles", "name": "system:aggregate-to-admin"},
	})
	if result.IsError {
		t.Fatalf("mark refused a real RBAC name: %s", toolText(result))
	}
	marks, _ := deps.State.Marks()
	if marks["admin"].Name != "system:aggregate-to-admin" {
		t.Errorf("marks = %+v, want admin on system:aggregate-to-admin", marks)
	}
}
