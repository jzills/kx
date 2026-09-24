package cli

import (
	"encoding/json"
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
		Config:    config.Default(),
		mu:        &sync.Mutex{},
		scanSlots: make(chan struct{}, 1),
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

// kubectl reads "secrets." and "secrets.v1." as core/v1 Secrets, but
// kinds.Normalize passes them through verbatim — so every check keyed on the
// canonical kind (redaction, the cluster-scope table) would miss them, and a
// mark would store the odd spelling. A core-group dotted spelling is reduced
// to its plain form first; a real API group is left alone.
func TestResolveTargetNormalisesCoreGroupDottedSpellings(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	for spelling, want := range map[string]kinds.Kind{
		"secrets.":                     kinds.Secret,
		"secrets.v1.":                  kinds.Secret,
		"secret.v1.":                   kinds.Secret,
		"Secret.v1.":                   kinds.Secret,
		"pods.v1.":                     kinds.Pod,
		"nodes.":                       kinds.Node,
		"certificates.cert-manager.io": "certificates.cert-manager.io",
		"deployments.v1.apps":          "deployments.v1.apps",
	} {
		got, err := deps.resolveTarget(mcpTarget{Kind: spelling, Name: "x"})
		if err != nil {
			t.Errorf("%s: %v", spelling, err)
			continue
		}
		if got.Kind != want {
			t.Errorf("%s: kind = %q, want %q", spelling, got.Kind, want)
		}
	}
	if got, _ := deps.resolveTarget(mcpTarget{Kind: "nodes.", Name: "n1"}); got.Namespace != "" {
		t.Errorf("nodes.: namespace = %q, want none for a cluster-scoped kind", got.Namespace)
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

// An index target resolves through state.Service exactly as `kx describe 2`
// would: the second row of whatever the user's terminal last listed, with the
// kind, namespace and context that listing stamped.
func TestResolveTargetResolvesAnIndex(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	if err := deps.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "api", Kind: kinds.Deployment, Namespace: "prod"},
			{Name: "web", Kind: kinds.Deployment, Namespace: "staging"},
		}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := deps.resolveTarget(mcpTarget{Index: 2})
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if got.Kind != kinds.Deployment || got.Name != "web" || got.Namespace != "staging" {
		t.Errorf("got %+v, want Deployment/web in staging (the second row)", got)
	}
}

// Out of range, a literal zero (indistinguishable from an omitted index) and
// no state at all each answer with kx's existing sentence for the failure,
// rather than a new one invented for the server.
func TestResolveTargetIndexFailuresGiveExistingSentences(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	if _, err := deps.resolveTarget(mcpTarget{Index: 1}); err == nil || !strings.Contains(err.Error(), "No state found") {
		t.Errorf("no state: err = %v, want the no-state sentence", err)
	}
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := deps.resolveTarget(mcpTarget{Index: 99}); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("out of range: err = %v, want the out-of-range sentence", err)
	}
	// A literal 0 carries no kind, name or mark either, so it reads as an
	// empty target — the same case "nothing" covers below — rather than as an
	// index, since omitempty makes the two indistinguishable on the wire.
	if _, err := deps.resolveTarget(mcpTarget{Index: 0}); err == nil {
		t.Errorf("index 0: resolved, want a refusal")
	}
}

// A listing taken in another context must never be spent here: the resource
// it names may exist in this cluster too, under a different identity, so
// resolving it would silently act on the wrong thing. Nothing may reach
// kubectl before this is caught.
func TestResolveTargetRefusesAnIndexFromAnotherContext(t *testing.T) {
	kube := &recordingKubectl{}
	deps := mcpTestDeps(t, kube)
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod),
		Namespace: "prod",
		Context:   "a",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := deps.resolveTarget(mcpTarget{Index: 1})
	if err == nil || !strings.Contains(err.Error(), "listed in context") {
		t.Fatalf("err = %v, want a context-mismatch refusal", err)
	}
	if len(kube.runs) != 0 || len(kube.probes) != 0 {
		t.Errorf("kubectl ran %v / probed %v, want none before a mismatch is caught", kube.runs, kube.probes)
	}
}

// A target names exactly one of kind/name, a mark or an index — mixing any
// two is refused with one sentence, before kubectl is ever asked anything.
func TestResolveTargetRefusesMixingIndexWithMarkOrKindName(t *testing.T) {
	kube := &recordingKubectl{}
	deps := mcpTestDeps(t, kube)
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := "A target is one of kind and name, a mark, or an index — give only one."
	for name, target := range map[string]mcpTarget{
		"index and mark":      {Index: 1, Mark: "api"},
		"index and kind/name": {Index: 1, Kind: "pods", Name: "api"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := deps.resolveTarget(target)
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %v before the mix was refused", kube.runs)
	}
}

// Every field a mark or an index hands back is held to the same shape a typed
// target is: a stored name or kind that reads as a kubectl flag must be
// refused before it reaches argv, exactly as a typed one is — closing the gap
// where a mark or an index's fields were trusted outright.
func TestResolveTargetValidatesResolvedFields(t *testing.T) {
	t.Run("index", func(t *testing.T) {
		kube := &recordingKubectl{}
		deps := mcpTestDeps(t, kube)
		if err := deps.State.Save(state.State{
			Resources: state.NewResources([]string{"-lapp=api"}, kinds.Pod), Namespace: "prod",
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if _, err := deps.resolveTarget(mcpTarget{Index: 1}); err == nil {
			t.Fatal("resolved a flag-shaped stored name, want a refusal")
		}
		if len(kube.runs) != 0 || len(kube.probes) != 0 {
			t.Errorf("kubectl ran %v / probed %v before validation refused it", kube.runs, kube.probes)
		}
	})
	t.Run("mark", func(t *testing.T) {
		kube := &recordingKubectl{}
		deps := mcpTestDeps(t, kube)
		if err := deps.State.SaveMark("bad", state.Mark{
			Resource: state.Resource{Name: "api", Kind: "--server=x", Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}
		if _, err := deps.resolveTarget(mcpTarget{Mark: "bad"}); err == nil {
			t.Fatal("resolved a flag-shaped stored kind, want a refusal")
		}
		if len(kube.runs) != 0 || len(kube.probes) != 0 {
			t.Errorf("kubectl ran %v / probed %v before validation refused it", kube.runs, kube.probes)
		}
	})
}

// A CRD's own kind is stored dotted, not as a kubectl shorthand — validation
// must not mistake that shape for something flag-like and refuse a
// legitimate mark or index.
func TestResolveTargetAcceptsAStoredCRDKind(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{})
	if err := deps.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "web-tls", Kind: "certificates.cert-manager.io", Namespace: "prod"},
		}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := deps.resolveTarget(mcpTarget{Index: 1})
	if err != nil {
		t.Fatalf("resolveTarget by index: %v", err)
	}
	if got.Kind != "certificates.cert-manager.io" || got.Name != "web-tls" {
		t.Errorf("got %+v, want the stored CRD kind", got)
	}

	if err := deps.State.SaveMark("cert", state.Mark{
		Resource: state.Resource{Name: "web-tls", Kind: "certificates.cert-manager.io", Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	got, err = deps.resolveTarget(mcpTarget{Mark: "cert"})
	if err != nil {
		t.Fatalf("resolveTarget by mark: %v", err)
	}
	if got.Kind != "certificates.cert-manager.io" {
		t.Errorf("got %+v, want the stored CRD kind", got)
	}
}

// The index itself is never echoed back: a client sees the kind, name and
// namespace it resolved to and nothing that looks like a spendable number.
func TestResolvedIndexTargetIsNotEchoedInOutput(t *testing.T) {
	kube := &recordingKubectl{output: "deployment.apps/api\n"}
	deps := mcpTestDeps(t, kube)
	if err := deps.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Deployment), Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	result := callTool(t, connectMCP(t, deps), "mark", map[string]any{
		"name": "culprit", "target": map[string]any{"index": 1},
	})
	if result.IsError {
		t.Fatalf("mark refused an index target: %s", toolText(result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"index"`) {
		t.Errorf("output = %s, want no index key", raw)
	}
}
