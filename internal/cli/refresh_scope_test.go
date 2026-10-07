package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// mismatchClusterServices is a listing taken in prod in context staging,
// with the caller since switched to production, whose namespace is other —
// and a cluster for client-go to read, which a sweep or a tree is re-run
// against.
func mismatchClusterServices(
	t *testing.T, kube *fakeKubectl, query *state.Query, objects ...runtime.Object,
) Services {
	t.Helper()
	store := &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")}
	store.Context = func() string { return "staging" }
	if err := store.Save(state.State{
		Resources: state.NewResources([]string{"api-old"}, kinds.Pod),
		Namespace: "prod",
		Query:     query,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	store.Context = func() string { return "production" }
	client := fake.NewSimpleClientset(objects...)
	return Services{
		Kubectl: kube, State: store, Index: index.Service{}, Config: config.Default(),
		Kubernetes: func() (kubernetes.Interface, error) { return client, nil },
	}
}

// runMismatch spends an index counted in the other context, returning what
// reached the screen.
func runMismatch(t *testing.T, services Services) string {
	t.Helper()
	out := captureRender(t)
	cmd := withRefresh(services, &cobra.Command{
		Use: "describe",
		RunE: func(*cobra.Command, []string) error {
			_, _, _, err := services.State.Fields(1)
			return err
		},
	})
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("an index from another context resolved")
	}
	return out.String()
}

// A namespace a listing inherited from its context belongs to that context.
// Spent after a switch, an index is refreshed against the context the user
// is in now, so the listing is run there as typed — in that context's own
// namespace. Replayed in the namespace it was taken in, kx get pods in
// staging's prod, then kx describe 1 in production, listed production's
// prod: on another cluster usually a namespace that does not exist, so the
// refresh found nothing where the user's own namespace was full.
func TestAContextSwitchRelistsInTheNewContextsNamespace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		query   *state.Query
		output  string
		objects []runtime.Object
		call    string
		want    []string
		absent  string
	}{
		{name: "kx get", query: &state.Query{Resource: "pods", Args: []string{}}, output: podsOutput,
			call: "get pods", want: []string{"Pods · other · 2 items"}},
		{name: "kx top", query: &state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}},
			output: "NAME      CPU(cores)   MEMORY(bytes)\napi-new   1m           2Mi\n",
			call:   "top pods", want: []string{"Pods · other · 1 item"}},
		{name: "kx diag", query: &state.Query{Command: state.CommandDiag, Args: []string{}},
			objects: []runtime.Object{brokenDeployment("api", "other"), brokenDeployment("db", "prod")},
			want:    []string{"· other ·", "api"}, absent: "db"},
		{name: "kx tree", query: &state.Query{Command: state.CommandTree, Args: []string{}},
			objects: []runtime.Object{healthyDeployment("api", "other"), healthyDeployment("db", "prod")},
			want:    []string{"Namespace/other", "Deployment/api"}, absent: "Deployment/db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &fakeKubectl{output: tc.output, namespace: "other"}
			services := mismatchClusterServices(t, kube, tc.query, tc.objects...)
			out := runMismatch(t, services)

			if tc.call != "" {
				if len(kube.calls) == 0 {
					t.Fatalf("kubectl was never run; output %q", out)
				}
				if got := joinArgs(kube.calls[len(kube.calls)-1]); got != tc.call {
					t.Errorf("kubectl %q, want %q — the listing as typed, in production's namespace", got, tc.call)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q\n  missing %q", out, want)
				}
			}
			if tc.absent != "" && strings.Contains(out, tc.absent) {
				t.Errorf("output = %q, holds %q from staging's namespace", out, tc.absent)
			}
			if entry := currentEntry(t, services); entry.Namespace != "other" {
				t.Errorf("refreshed entry namespace = %q, want other", entry.Namespace)
			}
		})
	}
}

// A namespace named with the listing is part of the command, and the command
// is what a context switch runs again: kx get pods -n prod is refreshed in
// production's prod.
func TestAContextSwitchKeepsANamedNamespace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		query   *state.Query
		output  string
		objects []runtime.Object
		call    string
		want    string
	}{
		{name: "kx get", query: &state.Query{Resource: "pods", Args: []string{"-n", "prod"}}, output: podsOutput,
			call: "get pods -n prod", want: "Pods · prod · 2 items"},
		{name: "kx diag", query: &state.Query{Command: state.CommandDiag, Args: []string{"-n", "prod"}},
			objects: []runtime.Object{brokenDeployment("db", "prod")}, want: "· prod ·"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &fakeKubectl{output: tc.output, namespace: "other"}
			out := runMismatch(t, mismatchClusterServices(t, kube, tc.query, tc.objects...))
			if tc.call != "" {
				if got := joinArgs(kube.calls[len(kube.calls)-1]); got != tc.call {
					t.Errorf("kubectl %q, want %q", got, tc.call)
				}
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %q\n  missing %q", out, tc.want)
			}
		})
	}
}

// When the refresh after a switch fails too, the command it names is the
// listing as typed: run where the user is now, it lists that context's
// namespace, as the refresh would have.
func TestAFailedRefreshAfterAContextSwitchNamesTheListingAsTyped(t *testing.T) {
	for _, tc := range []struct {
		query *state.Query
		want  string
	}{
		{&state.Query{Resource: "pods", Args: []string{}}, "Run 'kx get pods' to refresh the list."},
		{&state.Query{Resource: "pods", Args: []string{"-n", "prod"}}, "Run 'kx get pods -n prod' to refresh the list."},
		{&state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{}}, "Run 'kx top' to refresh the list."},
	} {
		kube := &fakeKubectl{err: errors.New("connection refused"), namespace: "other"}
		if out := runMismatch(t, mismatchClusterServices(t, kube, tc.query)); !strings.Contains(out, tc.want) {
			t.Errorf("output = %q\n  want %q", out, tc.want)
		}
	}
}

// A sweep or a tree in the namespace its context gave it is refreshed there,
// in the context it was taken in, as kx get's is — whatever namespace the
// caller has switched to since.
func TestAStaleSweepOrTreeIsRefreshedWhereItWasTaken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		query   *state.Query
		objects []runtime.Object
		want    string
	}{
		{"kx diag", &state.Query{Command: state.CommandDiag, Args: []string{}},
			[]runtime.Object{brokenDeployment("db", "prod")}, "· prod ·"},
		{"kx tree", &state.Query{Command: state.CommandTree, Args: []string{}},
			[]runtime.Object{healthyDeployment("db", "prod")}, "Namespace/prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleClusterServices(t, &fakeKubectl{namespace: "kube-system"}, tc.query, tc.objects...)
			if out := runStale(t, services); !strings.Contains(out, tc.want) {
				t.Errorf("output = %q\n  missing %q", out, tc.want)
			}
			history, err := services.State.LoadHistory()
			if err != nil {
				t.Fatalf("LoadHistory: %v", err)
			}
			if len(history.States) != 1 || history.States[0].Namespace != "prod" {
				t.Errorf("history = %+v, want the stale entry replaced by prod's", history.States)
			}
		})
	}
}

func diagCommand(services Services) *cobra.Command {
	return newDiagnosticCommand(services, "diag", nil)
}

// A sweep and a tree record a namespace in their query only when one was
// named, as kx get does: the namespace the context gave them is the entry's,
// not the command's, so that a context switch can tell the two apart.
func TestSweepsAndTreesRecordANamespaceOnlyWhenNamed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func(Services) *cobra.Command
		args    []string
		want    []string
	}{
		{"kx diag", diagCommand, nil, []string{}},
		{"kx diag -n prod", diagCommand, []string{"-n", "prod"}, []string{"-n", "prod"}},
		{"kx tree", newTreeCommand, nil, []string{}},
		{"kx tree -n prod", newTreeCommand, []string{"-n", "prod"}, []string{"-n", "prod"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleClusterServices(t, &fakeKubectl{namespace: "prod"}, nil, healthyDeployment("web", "prod"))
			if _, _, err := runCaptured(t, tc.command(services), tc.args); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			entry := currentEntry(t, services)
			if entry.Query == nil || strings.Join(entry.Query.Args, " ") != strings.Join(tc.want, " ") {
				t.Errorf("query = %+v, want args %q", entry.Query, tc.want)
			}
			if entry.Namespace != "prod" {
				t.Errorf("entry namespace = %q, want prod", entry.Namespace)
			}
		})
	}
}

// A command whose output a program reads is not refreshed, only told what to
// run (reportStale) — the same command a failed refresh names, so after a
// context switch the listing as typed.
func TestAContextSwitchUnderMachineOutputNamesTheListingAsTyped(t *testing.T) {
	services := mismatchClusterServices(t, &fakeKubectl{output: podsOutput, namespace: "other"},
		&state.Query{Resource: "pods", Args: []string{}})
	cmd := withRefresh(services, &cobra.Command{
		Use: "get", DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error {
			_, _, _, err := services.State.Fields(1)
			return err
		},
	})
	stdout, stderr, err := runCaptured(t, cmd, []string{"-o", "json"})
	if err == nil {
		t.Fatal("an index from another context resolved")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing ahead of the JSON a script reads", stdout)
	}
	if !strings.Contains(stderr, "Run 'kx get pods' to refresh the list.") {
		t.Errorf("stderr = %q, want the listing as typed", stderr)
	}
}
