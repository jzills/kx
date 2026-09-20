package cli

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/graph"
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

// Every command that takes a state.Ref resolves through fakeResolver in its
// tests, and fakeResolver.Resolve used to ignore its argument entirely —
// Fields does too, always returning the same fixed name/namespace/kind no
// matter what int (or Ref) it was handed. That meant no test anywhere could
// tell Execute(ref, …) apart from Execute(state.Ref{Index: ref.Index}, …):
// both resolved identically, so a call site that silently dropped a mark on
// its way to Resolve would pass every existing test.
//
// This drives a mark-bearing Ref into every command whose Execute method
// takes one (Describe is covered separately via forwardExit in
// refresh_test.go, and MetadataRead was removed as dead code) and asserts
// fakeResolver actually saw that Ref, unmodified, in every one of its Resolve
// calls — not just the first. A command that rebuilt its Ref from a bare
// index before calling Resolve would hand fakeResolver a zero-Mark Ref
// instead and fail here. Checking every recorded call, not only the first,
// is what catches MetadataWriteCommand: it resolves twice (once directly,
// once again inside fetchMetadataField's conflict check), and only the
// second of those calls was the one a prior fix round missed.
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
		"edit": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := EditCommand{Kubectl: &recordingKubectl{}, State: resolver}
				return command.Execute(mark, nil)
			},
		},
		"scale": {
			kind: kinds.Deployment,
			run: func(resolver fakeResolver) error {
				command := ScaleCommand{Kubectl: &recordingKubectl{}, State: resolver}
				_, err := command.Execute(mark, 3, nil)
				return err
			},
		},
		"rollout": {
			kind: kinds.Deployment,
			run: func(resolver fakeResolver) error {
				command := RolloutCommand{Kubectl: &recordingKubectl{}, State: resolver}
				_, err := command.Execute("restart", mark, nil)
				return err
			},
		},
		"port-forward": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := PortForwardCommand{Kubectl: &recordingKubectl{}, State: resolver}
				return command.Execute(mark, "8080:80", nil)
			},
		},
		"exec": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := ExecCommand{Kubectl: &recordingKubectl{}, State: resolver}
				return command.Execute(mark, []string{"true"}, nil)
			},
		},
		"debug": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := DebugCommand{Kubectl: &recordingKubectl{}, State: resolver, Image: "busybox"}
				return command.Execute(mark, nil, nil)
			},
		},
		"tree": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				client := fake.NewSimpleClientset(&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "prod"},
				})
				command := TreeCommand{Builder: graph.Builder{Client: client}, State: resolver}
				_, err := command.Execute(context.Background(), mark, false)
				return err
			},
		},
		"scan": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := ScanCommand{
					Kubectl: &recordingKubectl{
						output: `{"kind":"Pod","spec":{"containers":[{"image":"api:v1"}]}}`,
					},
					State:   resolver,
					Scanner: &fakeScanner{},
					Status:  noStatus,
				}
				_, err := command.Execute(mark, "grype")
				return err
			},
		},
		"diagnostic": {
			kind: kinds.Deployment,
			run: func(resolver fakeResolver) error {
				command := DiagnosticCommand{State: resolver, Diagnostics: &fakeGatherer{}}
				_, err := command.Execute(context.Background(), mark)
				return err
			},
		},
		"metadata-write": {
			kind: kinds.Pod,
			run: func(resolver fakeResolver) error {
				command := MetadataWriteCommand{
					Kubectl: &recordingKubectl{output: `{"metadata":{"name":"target"}}`},
					State:   resolver, Verb: "label", Field: "labels",
				}
				_, err := command.Execute(mark, []string{"env"}, map[string]string{"env": "prod"}, nil, false)
				return err
			},
		},
		"drain": {
			kind: kinds.Node,
			run: func(resolver fakeResolver) error {
				command := DrainCommand{
					Kubectl: &recordingKubectl{}, State: resolver,
					Confirm: func(string) error { return nil },
				}
				return command.Execute(mark, true, nil)
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
			for i, got := range *seen {
				if got != mark {
					t.Errorf("Resolve call %d saw %#v, want the caller's own Ref %#v unchanged", i, got, mark)
				}
			}
		})
	}
}

// The table above proves each command's own Execute method forwards its Ref
// to Resolve unchanged. It says nothing about the RunE closures that call
// Execute: describe, delete, yaml and kx cordon all resolve a batch through
// resolveRefs and then hand each target.Ref to Execute one at a time, and
// decodeSecrets does the same for `kx secret --decode`; kx events does it for
// a single target. A RunE that rebuilt state.Ref{Index: target.Ref.Index}
// there instead would pass every case in the table above untouched — Execute
// itself never sees the difference — while silently spending index 0 for
// every mark given on the command line. This drives the real cobra commands
// (or, for decodeSecrets, the function runGet calls directly, matching how
// TestDecodeRefusesTheBatchBeforePrintingAnySecret already exercises it) end
// to end, with a real state.Service holding a saved mark, and checks what
// actually reached kubectl.
func TestRunEHandOffsThreadAMarkToKubectl(t *testing.T) {
	t.Run("describe", func(t *testing.T) {
		kube := &recordingKubectl{}
		services := switchServices(t, kube)
		if err := services.State.SaveMark("api", state.Mark{
			Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}

		cmd := newDescribeCommand(services)
		cmd.SetArgs([]string{"@api"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("kx describe @api: %v", err)
		}
		if len(kube.interactive) != 1 {
			t.Fatalf("kubectl invoked %d times, want 1", len(kube.interactive))
		}
		if got := joinArgs(kube.interactive[0]); !strings.Contains(got, "api-7d8f") || !strings.Contains(got, "-n prod") {
			t.Errorf("kubectl args = %q, want the marked resource in prod", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		kube := &recordingKubectl{}
		services := switchServices(t, kube)
		services.Confirm = func(string) error { return nil }
		if err := services.State.SaveMark("api", state.Mark{
			Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}

		cmd := newDeleteCommand(services)
		cmd.SetArgs([]string{"@api", "-y"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("kx delete @api -y: %v", err)
		}
		if len(kube.runs) != 1 {
			t.Fatalf("kubectl invoked %d times, want 1", len(kube.runs))
		}
		if got := joinArgs(kube.runs[0]); !strings.Contains(got, "api-7d8f") || !strings.Contains(got, "-n prod") {
			t.Errorf("kubectl args = %q, want the marked resource in prod", got)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		kube := &recordingKubectl{output: "a: b"}
		services := switchServices(t, kube)
		if err := services.State.SaveMark("api", state.Mark{
			Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Deployment, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}

		cmd := newYamlCommand(services)
		cmd.SetArgs([]string{"@api"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("kx yaml @api: %v", err)
		}
		if len(kube.runs) != 1 {
			t.Fatalf("kubectl invoked %d times, want 1", len(kube.runs))
		}
		if got := joinArgs(kube.runs[0]); !strings.Contains(got, "api-7d8f") || !strings.Contains(got, "-n prod") {
			t.Errorf("kubectl args = %q, want the marked resource in prod", got)
		}
	})

	t.Run("cordon", func(t *testing.T) {
		kube := &recordingKubectl{}
		services := switchServices(t, kube)
		if err := services.State.SaveMark("worker", state.Mark{
			Resource: state.Resource{Name: "node-a", Kind: kinds.Node},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}

		cmd := newCordonCommand(services, "cordon")
		cmd.SetArgs([]string{"@worker"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("kx cordon @worker: %v", err)
		}
		if len(kube.runs) != 1 {
			t.Fatalf("kubectl invoked %d times, want 1", len(kube.runs))
		}
		if got := joinArgs(kube.runs[0]); got != "cordon node-a" {
			t.Errorf("kubectl args = %q, want %q", got, "cordon node-a")
		}
	})

	t.Run("secret_decode", func(t *testing.T) {
		kube := &recordingKubectl{output: `{"data":{"key":"dmFsdWU="}}`}
		services := switchServices(t, kube)
		if err := services.State.SaveMark("api", state.Mark{
			Resource: state.Resource{Name: "api-secret", Kind: kinds.Secret, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}
		captureRender(t)

		if err := runGet(services, "secret", []string{"@api"}, getOptions{Decode: true, Yes: true}); err != nil {
			t.Fatalf("kx secret @api --decode: %v", err)
		}
		if len(kube.runs) != 1 {
			t.Fatalf("kubectl invoked %d times, want 1", len(kube.runs))
		}
		if got := joinArgs(kube.runs[0]); !strings.Contains(got, "api-secret") || !strings.Contains(got, "-n prod") {
			t.Errorf("kubectl args = %q, want the marked secret in prod", got)
		}
	})

	t.Run("events", func(t *testing.T) {
		kube := &recordingKubectl{}
		services := switchServices(t, kube)
		if err := services.State.SaveMark("api", state.Mark{
			Resource: state.Resource{Name: "api-7d8f", Kind: kinds.Pod, Namespace: "prod"},
		}); err != nil {
			t.Fatalf("SaveMark: %v", err)
		}
		out := captureRender(t)
		event := &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "api-7d8f.Started", Namespace: "prod"},
			InvolvedObject: corev1.ObjectReference{Name: "api-7d8f", Kind: "Pod"},
			Reason:         "Started",
			Type:           "Normal",
			Message:        "Started",
		}
		client := fake.NewSimpleClientset(event)
		services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }

		cmd := newEventsCommand(services)
		cmd.SetArgs([]string{"@api"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("kx events @api: %v", err)
		}
		if !strings.Contains(out.String(), "api-7d8f") {
			t.Errorf("output = %q, want the marked resource named", out.String())
		}
	})
}
