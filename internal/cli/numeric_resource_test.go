package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// A number in kx get's resource position names a row, not a kind.
//
// `kx get pods 1 2` re-fetches rows 1 and 2, so `kx get 1 2` is the mistake
// someone who knows `kx describe 1 2` makes. Read as typed, the 1 went
// through as the resource: kubectl was asked for a resource type called "1",
// and with a listing saved kx reported a kind mismatch against that "1" and
// suggested `kx get 1` — the same mistake again, and not a command that can
// work. No Kubernetes kind is spelled as a number, so this is unambiguous,
// and kx already refuses the mirror of it in `kx mark 1 webpod`.
//
// The suggestion names the current listing's own kind, because that is the
// command the caller wanted.
func TestANumberInTheResourcePositionNamesARow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		kind  kinds.Kind
		names []string
		want  []string
	}{
		{
			name:  "with a listing of one kind, names it",
			args:  []string{"get", "1", "2"},
			kind:  kinds.Pod,
			names: []string{"api", "web"},
			want:  []string{"'1' names a row", "kx get pods 1 2"},
		},
		{
			name:  "a single index too",
			args:  []string{"get", "1"},
			kind:  kinds.Deployment,
			names: []string{"api"},
			want:  []string{"'1' names a row", "kx get deployments 1"},
		},
		{
			name: "with no listing to name a kind, teaches the shape",
			args: []string{"get", "1", "2"},
			want: []string{"'1' names a row", "kx get <resource> 1 2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := argvServices(t)
			if tc.kind != "" {
				if err := services.State.Save(state.State{
					Resources: state.NewResources(tc.names, tc.kind),
					Namespace: "prod",
					Query:     &state.Query{Resource: "pods", Args: []string{}},
				}); err != nil {
					t.Fatalf("seed state: %v", err)
				}
			}

			err := Execute(NewRoot(services, "test"), tc.args)

			if err == nil {
				t.Fatalf("kx %s succeeded", strings.Join(tc.args, " "))
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to contain %q", err, want)
				}
			}
			// The old answer suggested a command that is the same mistake.
			if strings.Contains(err.Error(), "run 'kx get 1'") {
				t.Errorf("err = %q, which suggests the mistake again", err)
			}
		})
	}
}

// A resource that is not a number is still kubectl's to judge: kx knows
// nothing about which CRDs a cluster has, so anything kind-shaped goes
// through and kubectl answers for it.
func TestAKindShapedResourceStillReachesKubectl(t *testing.T) {
	services := argvServices(t)
	kube := &recordingKubectl{output: podsOutput}
	services.Kubectl = kube

	if err := Execute(NewRoot(services, "test"), []string{"get", "widgets.example.com"}); err != nil {
		t.Fatalf("kx get widgets.example.com: %v", err)
	}
	if len(kube.runs) == 0 || !strings.Contains(joinArgs(kube.runs[0]), "widgets.example.com") {
		t.Errorf("kubectl calls = %v, want the resource passed through", kube.runs)
	}
}
