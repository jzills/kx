package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/index"
)

// The diagnose sweep narrows by the same term kx diag -m does, and its
// document names it.
func TestMCPDiagnoseSweepTakesAMatch(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	deps.Kubernetes = func() (kubernetes.Interface, error) {
		return fake.NewSimpleClientset(
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "prod"}},
		), nil
	}
	_, out, err := deps.diagnose(context.Background(), &mcp.CallToolRequest{},
		diagnoseInput{Match: "api", Full: true})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if out.Diagnosis.Match != "api" || out.Diagnosis.Checked != 1 ||
		len(out.Diagnosis.Resources) != 1 || out.Diagnosis.Resources[0].Name != "api" {
		t.Errorf("diagnosis = %+v, want api alone, under match api", out.Diagnosis)
	}
}

// The tree tool narrows a namespace walk to the matched roots.
func TestMCPTreeTakesAMatch(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	deps.Kubernetes = func() (kubernetes.Interface, error) { return matchForest(), nil }
	_, result, err := deps.tree(context.Background(), &mcp.CallToolRequest{},
		treeInput{Match: "worker"})
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	out := result.(treeOutput)
	if out.Tree.Match != "worker" || len(out.Tree.Roots) != 1 ||
		len(out.Tree.Roots[0].Children) != 1 || out.Tree.Roots[0].Children[0].Name != "worker" {
		t.Errorf("tree = %+v, want prod holding worker alone", out.Tree)
	}
}

// The scan sweep reads only the matched workloads' images.
func TestMCPScanSweepTakesAMatch(t *testing.T) {
	scanner := &fakeScanner{}
	deps, _ := scanDeps(t, scanner, matchScanItems)
	_, out, err := deps.scan(context.Background(), &mcp.CallToolRequest{},
		scanInput{Match: "worker"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.Scan.Match != "worker" || len(out.Scan.Images) != 1 || out.Scan.Images[0].Image != "worker:v2" {
		t.Errorf("scan = %+v, want worker:v2 alone, under match worker", out.Scan)
	}
}

// A target already names one resource, so a term beside it is refused by
// every tool that takes both, before the cluster is read.
func TestMCPMatchIsRefusedBesideATarget(t *testing.T) {
	deps := mcpTestDeps(t, &recordingKubectl{namespace: "prod"})
	target := &mcpTarget{Kind: "deploy", Name: "api"}
	ctx, request := context.Background(), &mcp.CallToolRequest{}
	for name, call := range map[string]func() error{
		"diagnose": func() error {
			_, _, err := deps.diagnose(ctx, request, diagnoseInput{Target: target, Match: "api"})
			return err
		},
		"tree": func() error {
			_, _, err := deps.tree(ctx, request, treeInput{Target: target, Match: "api"})
			return err
		},
		"scan": func() error {
			_, _, err := deps.scan(ctx, request, scanInput{Target: target, Match: "api"})
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "'match'") {
			t.Errorf("%s with a target and a match: err = %v, want the refusal", name, err)
		}
	}
}

// list_resources narrows by name as kx get -m does: the other three listing
// tools took a match and the one kx get mirrors did not. What it saves with
// --write-listings is kx get -m's listing, term included.
func TestMCPListResourcesTakesAMatch(t *testing.T) {
	deps := writingDeps(t, &recordingKubectl{output: podsOutput, namespace: "prod"})
	var out listOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "pods", "match": "REDIS"}), &out)
	if out.Match != "REDIS" || out.Total != 1 || len(out.Resources) != 1 ||
		out.Resources[0].Name != "redis-def-uvw" || out.Resources[0].Index != 1 {
		t.Errorf("out = %+v, want redis alone, at index 1, under its match", out)
	}

	entry := onlyTaggedEntry(t, deps.State)
	cli := cliState(t)
	if _, _, err := (GetCommand{
		Kubectl: &recordingKubectl{output: podsOutput, namespace: "prod"}, State: cli, Index: index.Service{},
	}).Execute("pods", "REDIS", []string{"-n", "prod"}); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
}

// An agent's term is saved with its listing under --write-listings, and kx
// prints a saved term back to the user's terminal — kx state's caption for an
// emptied listing, the refusal of an index into one, the command a failed
// refresh names. A term holding an escape sequence wrote it to that terminal,
// so every tool that takes one refuses a control character, before the
// cluster is read and before anything is saved. No resource name holds one,
// so nothing that could match is refused.
func TestMCPMatchRefusesAControlCharacter(t *testing.T) {
	for _, term := range []string{"zz\x1b]0;pwned\x07zz", "zz\u009b2Jzz", "zz\nzz"} {
		kube := &recordingKubectl{output: podsOutput, namespace: "prod"}
		deps := writingDeps(t, kube)
		deps.Scanner = &fakeScanner{}
		deps.Kubernetes = func() (kubernetes.Interface, error) {
			t.Error("the cluster was read for a term that is refused")
			return fake.NewSimpleClientset(), nil
		}
		ctx, request := context.Background(), &mcp.CallToolRequest{}
		for name, call := range map[string]func() error{
			"list_resources": func() error {
				_, _, err := deps.listResources(ctx, request, listInput{Kind: "pods", Match: term})
				return err
			},
			"diagnose": func() error {
				_, _, err := deps.diagnose(ctx, request, diagnoseInput{Match: term})
				return err
			},
			"tree": func() error {
				_, _, err := deps.tree(ctx, request, treeInput{Match: term})
				return err
			},
			"scan": func() error {
				_, _, err := deps.scan(ctx, request, scanInput{Match: term})
				return err
			},
		} {
			err := call()
			if err == nil || !strings.Contains(err.Error(), "control character") {
				t.Errorf("%s with match %q: err = %v, want the refusal", name, term, err)
			}
			if err != nil && strings.Contains(err.Error(), term) {
				t.Errorf("%s echoed the term back: %q", name, err)
			}
		}
		if len(kube.runs) > 0 {
			t.Errorf("kubectl ran %v for a term that is refused", kube.runs)
		}
		if _, err := deps.State.Load(); err == nil {
			t.Errorf("a listing was saved for match %q", term)
		}
	}
}

// What a name can hold is accepted, including what a DNS name cannot: RBAC
// names such as system:controller:job-controller are path segments, and the
// term is matched case-insensitively.
func TestMCPMatchAcceptsWhatANameCanHold(t *testing.T) {
	for _, term := range []string{"system:controller", "API", "web.v2", "a_b"} {
		if err := validMatch(term); err != nil {
			t.Errorf("validMatch(%q) = %v, want nil", term, err)
		}
	}
}

// Every tool that takes a namespace refuses one that is not a Kubernetes
// namespace, before the cluster is read and before anything is saved. diagnose
// and tree took it as given: under --write-listings an escape sequence in it
// was saved as the entry's namespace and in its query, and kx state and the
// command a failed refresh names wrote it to the user's terminal.
func TestMCPNamespaceIsValidatedByEveryToolThatTakesOne(t *testing.T) {
	const namespace = "x\x1b]0;pwned\x07"
	kube := &recordingKubectl{output: podsOutput, namespace: "prod"}
	deps := writingDeps(t, kube)
	deps.Scanner = &fakeScanner{}
	deps.Kubernetes = func() (kubernetes.Interface, error) {
		t.Error("the cluster was read for a namespace that is refused")
		return fake.NewSimpleClientset(), nil
	}
	ctx, request := context.Background(), &mcp.CallToolRequest{}
	for name, call := range map[string]func() error{
		"list_resources": func() error {
			_, _, err := deps.listResources(ctx, request, listInput{Kind: "pods", Namespace: namespace})
			return err
		},
		"diagnose": func() error {
			_, _, err := deps.diagnose(ctx, request, diagnoseInput{Namespace: namespace})
			return err
		},
		"tree": func() error {
			_, _, err := deps.tree(ctx, request, treeInput{Namespace: namespace})
			return err
		},
		"top": func() error {
			_, _, err := deps.top(ctx, request, topInput{Namespace: namespace})
			return err
		},
		"scan": func() error {
			_, _, err := deps.scan(ctx, request, scanInput{Namespace: namespace})
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "is not a Kubernetes namespace") {
			t.Errorf("%s: err = %v, want the namespace refused", name, err)
		}
	}
	if len(kube.runs) > 0 {
		t.Errorf("kubectl ran %v for a namespace that is refused", kube.runs)
	}
	if _, err := deps.State.Load(); err == nil {
		t.Error("a listing was saved under a namespace that is refused")
	}
}
