package cli

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// staleClusterServices is staleServices with a cluster for client-go to read,
// which a sweep or a tree is re-run against.
func staleClusterServices(
	t *testing.T, kube kubectl.Service, query *state.Query, objects ...runtime.Object,
) Services {
	t.Helper()
	services := staleServices(t, kube, query)
	client := fake.NewSimpleClientset(objects...)
	services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	return services
}

// runStale runs a command that fails as a vanished index does, returning
// what reached the screen.
func runStale(t *testing.T, services Services) string {
	t.Helper()
	out := captureRender(t)
	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	return out.String()
}

func currentQuery(t *testing.T, services Services) *state.Query {
	t.Helper()
	entry, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return entry.Query
}

// A stale index from kx top was refreshed into a kx get pods table: the
// query was recorded as get pods so the one replay there was could run it.
// The listing a user goes back to should be the one they were reading, so
// kx top is run again.
func TestStaleRefreshRunsKxTopAgain(t *testing.T) {
	kube := &fakeKubectl{
		output:    "NAME      CPU(cores)   MEMORY(bytes)\napi-new   1m           2Mi\n",
		namespace: "prod",
	}
	services := staleServices(t, kube,
		&state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}})

	out := runStale(t, services)
	if got := joinArgs(kube.args); got != "top pods" {
		t.Errorf("kubectl %q, want kx top's own call", got)
	}
	if !strings.Contains(out, "CPU(cores)") || !strings.Contains(out, "api-new") {
		t.Errorf("output = %q, want the usage table again", out)
	}
	if query := currentQuery(t, services); query == nil || query.Command != state.CommandTop {
		t.Errorf("current query = %+v, want kx top's", query)
	}
}

// A sweep had no query, so a stale index from one was answered "Run 'kx get
// <resource>'" — not the command that listed it. It is swept again.
func TestStaleRefreshSweepsAgain(t *testing.T) {
	services := staleClusterServices(t, &fakeKubectl{namespace: "prod"},
		&state.Query{Command: state.CommandDiag, Args: []string{"-n", "prod"}},
		brokenDeployment("api", "prod"), healthyDeployment("web", "prod"))

	out := runStale(t, services)
	for _, want := range []string{"State was stale", "Mixed · prod · 2 checked", "api"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q\n  missing %q", out, want)
		}
	}
	if strings.Contains(out, "to refresh the list") {
		t.Errorf("output = %q, hinted at a refresh that had just succeeded", out)
	}
	if query := currentQuery(t, services); query == nil || query.Command != state.CommandDiag {
		t.Errorf("current query = %+v, want the sweep's", query)
	}
}

// A tree is walked again: a namespace's as one, and one resource's from the
// resource at its root.
func TestStaleRefreshWalksTheTreeAgain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query *state.Query
		want  []string
	}{
		{"a namespace", &state.Query{Command: state.CommandTree, Args: []string{"-n", "prod"}},
			[]string{"Namespace/prod", "Deployment/web", "web-abc-1"}},
		{"one resource", &state.Query{Command: state.CommandTree, Resource: "Deployment/web", Args: []string{"-n", "prod"}},
			[]string{"Deployment/web", "web-abc-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleClusterServices(t, &fakeKubectl{namespace: "prod"}, tc.query, treeObjects()...)
			out := runStale(t, services)
			for _, want := range append([]string{"State was stale"}, tc.want...) {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q\n  missing %q", out, want)
				}
			}
			if query := currentQuery(t, services); query == nil || query.Command != state.CommandTree {
				t.Errorf("current query = %+v, want the walk's", query)
			}
		})
	}
}

// A replay that fails names the command that made the listing, not "kx get
// <resource>", which was wrong for every listing kx get did not make.
func TestStaleRefreshNamesTheListingsCommandWhenItCannotRunIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query *state.Query
		want  string
	}{
		{"kx get", &state.Query{Resource: "pods", Args: []string{}}, "Run 'kx get pods' to refresh the list."},
		{"kx top", &state.Query{Command: state.CommandTop, Resource: "nodes", Args: []string{}}, "Run 'kx top nodes' to refresh the list."},
		{"a sweep", &state.Query{Command: state.CommandDiag, Args: []string{"-A"}, Match: matchOf("api")},
			"Run 'kx diag -A -m api' to refresh the list."},
		{"a namespace's tree", &state.Query{Command: state.CommandTree, Args: []string{"-n", "prod"}},
			"Run 'kx tree -n prod' to refresh the list."},
		// The root itself may be what went: list its kind to find it again.
		{"one resource's tree", &state.Query{Command: state.CommandTree, Resource: "Deployment/web", Args: []string{"-n", "prod"}},
			"Run 'kx get deployments' to refresh the list."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleServices(t, &fakeKubectl{err: errors.New("connection refused")}, tc.query)
			services.Kubernetes = func() (kubernetes.Interface, error) {
				return nil, errors.New("connection refused")
			}
			if out := runStale(t, services); !strings.Contains(out, tc.want) {
				t.Errorf("output = %q\n  missing %q", out, tc.want)
			}
		})
	}
}
