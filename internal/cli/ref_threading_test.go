package cli

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// noEventsService answers every query with no events, so EventsCommand.Execute
// takes the empty-listing path (a staleness probe, nothing more) without a
// real API client. It satisfies events.Service structurally.
type noEventsService struct{}

func (noEventsService) Get(context.Context, string) ([]corev1.Event, error) { return nil, nil }
func (noEventsService) Filter([]corev1.Event, string, kinds.Kind) []corev1.Event {
	return nil
}

// resolverAndSeen builds a fakeResolver that records every Ref handed to
// Resolve, so a test can assert on the Ref a command actually passed through
// rather than trusting the fixed name/namespace/kind Fields returns regardless
// of the argument.
func resolverAndSeen(name, namespace string, kind kinds.Kind) (fakeResolver, *[]state.Ref) {
	seen := &[]state.Ref{}
	return fakeResolver{name: name, namespace: namespace, kind: kind, seen: seen}, seen
}

// The eight commands converted from a bare index to a state.Ref (Task 7b) all
// resolve through fakeResolver in their tests, and fakeResolver.Resolve used
// to ignore its argument entirely — Fields does too, always returning the same
// fixed name/namespace/kind no matter what int (or Ref) it was handed. That
// meant no test anywhere could tell Execute(ref, …) apart from
// Execute(state.Ref{Index: ref.Index}, …): both resolved identically, so a
// call site that silently dropped a mark on its way to Resolve would pass
// every existing test.
//
// This drives a mark-bearing Ref into each of the surviving six (Describe is
// covered separately via forwardExit in refresh_test.go, and MetadataRead was
// removed as dead code) and asserts fakeResolver actually saw that Ref,
// unmodified, in its Resolve call. A command that rebuilt its Ref from a bare
// index before calling Resolve would hand fakeResolver a zero-Mark Ref instead
// and fail here.
func TestExecuteMethodsPassTheirRefUnchangedToResolve(t *testing.T) {
	mark := state.Ref{Mark: "api"}

	cases := map[string]struct {
		kind kinds.Kind
		run  func(resolver fakeResolver) error
	}{
		"events": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := EventsCommand{
					Kubectl: &recordingKubectl{}, State: resolver, Events: noEventsService{},
				}
				_, err := command.Execute(context.Background(), mark)
				return err
			},
		},
		"secret": {
			kind: kinds.Secret,
			run: func(resolver fakeResolver) error {
				command := SecretCommand{Kubectl: &recordingKubectl{output: `{}`}, State: resolver}
				_, err := command.Execute(mark)
				return err
			},
		},
		"yaml": {
			kind: kinds.Deployment,
			run: func(resolver fakeResolver) error {
				command := YamlCommand{Kubectl: &recordingKubectl{output: "a: b"}, State: resolver}
				_, err := command.Execute(mark, nil, nil)
				return err
			},
		},
		"delete": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := DeleteCommand{Kubectl: &recordingKubectl{}, State: resolver, Status: noStatus}
				_, err := command.Execute(mark, true, nil)
				return err
			},
		},
		"node": {
			kind: kinds.Node,
			run: func(resolver fakeResolver) error {
				command := NodeCommand{Kubectl: &recordingKubectl{}, State: resolver, Verb: "cordon"}
				_, err := command.Execute(mark)
				return err
			},
		},
		"logs": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := LogsCommand{Kubectl: &recordingKubectl{}, State: resolver, Status: noStatus}
				return command.Execute(mark, nil)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resolver, seen := resolverAndSeen("target", "prod", tc.kind)
			if err := tc.run(resolver); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(*seen) == 0 {
				t.Fatal("Resolve was never called")
			}
			if got := (*seen)[0]; got != mark {
				t.Errorf("Resolve saw %#v, want the caller's own Ref %#v unchanged", got, mark)
			}
		})
	}
}
