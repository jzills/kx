package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// An index carries the namespace it was listed from, so a scope flag beside
// one contradicts it. Every other index command refuses that; kx get's fetch
// path did not, and forwarded the flag to kubectl instead.
//
// `kx get pods 1 -A` became a by-name lookup across all namespaces, which
// kubectl forbids outright, and `kx get pods 1 -n other` looked for a pod
// from one namespace in another and reported a NotFound naming the right pod
// and the wrong namespace. Neither said what was actually wrong.
//
// getbody.go already refused a *cluster* flag beside an index, and refused a
// scope flag on a cluster-scoped kind; the scope-flag-beside-index case was
// the one combination with no check.
func TestGetRefusesAScopeFlagBesideAnIndex(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"-A", []string{"pods", "1", "-A"}, "'-A' cannot be combined with an index"},
		{"--all-namespaces", []string{"pods", "1", "--all-namespaces"},
			"'--all-namespaces' cannot be combined with an index"},
		{"-n", []string{"pods", "1", "-n", "other"}, "'-n' cannot be combined with an index"},
		{"--namespace", []string{"pods", "1", "--namespace=other"},
			"'--namespace' cannot be combined with an index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &recordingKubectl{output: podsOutput}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			_, _, err := runCaptured(t, newGetCommand(services), tc.args)

			if err == nil {
				t.Fatalf("kx get %s succeeded", strings.Join(tc.args, " "))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want %q", err, tc.want)
			}
			// Refused before the cluster is read: the point of refusing a
			// contradiction is that nothing is fetched on one.
			if len(kube.runs) > 0 {
				t.Errorf("kubectl was called: %v", kube.runs)
			}
		})
	}
}

// The listing cases are untouched — with no index, a scope flag is the scope
// rather than a contradiction. This is the control that stops the refusal
// above from swallowing ordinary listings.
func TestGetWithAScopeFlagAndNoIndexStillLists(t *testing.T) {
	for _, args := range [][]string{
		{"pods", "-A"},
		{"pods", "-n", "other"},
		{"pods", "--all-namespaces"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			kube := &recordingKubectl{output: podsOutput}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})

			if _, _, err := runCaptured(t, newGetCommand(services), args); err != nil {
				t.Fatalf("kx get %s: %v", strings.Join(args, " "), err)
			}
			if len(kube.runs) == 0 {
				t.Error("kubectl was not called — the listing was refused")
			}
		})
	}
}

// A cluster-scoped kind keeps its own, more specific refusal rather than
// taking the beside-an-index one: there the flag is wrong because Nodes have
// no namespace, not because an index carries one.
func TestGetKeepsTheClusterScopedRefusalForAScopeFlag(t *testing.T) {
	kube := &recordingKubectl{output: podsOutput}
	services := staleServices(t, kube, &state.Query{Resource: "nodes", Args: []string{}})
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"node-a"}, kinds.Node),
		Query:     &state.Query{Resource: "nodes", Args: []string{}},
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	_, _, err := runCaptured(t, newGetCommand(services), []string{"nodes", "1", "-n", "other"})

	if err == nil {
		t.Fatal("kx get nodes 1 -n other succeeded")
	}
	if !strings.Contains(err.Error(), "Nodes") {
		t.Errorf("err = %q, want the cluster-scoped refusal naming the kind", err)
	}
}
