package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
)

// fakeResolver resolves indexes without touching a state file.
type fakeResolver struct {
	name      string
	namespace string
	kind      kinds.Kind
	err       error
}

func (f fakeResolver) Fields(int) (string, string, kinds.Kind, error) {
	if f.err != nil {
		return "", "", "", f.err
	}
	return f.name, f.namespace, f.kind, nil
}

func pod(name string) fakeResolver {
	return fakeResolver{name: name, namespace: "prod", kind: kinds.Pod}
}

func workload(name string, kind kinds.Kind) fakeResolver {
	return fakeResolver{name: name, namespace: "prod", kind: kind}
}

// recordingKubectl captures every invocation so tests can assert on the exact
// kubectl command line kx builds.
type recordingKubectl struct {
	runs        [][]string
	interactive [][]string
	probes      [][]string
	output      string
	err         error
	exitCode    int
	probeCode   int
	quietStderr bool
}

func (k *recordingKubectl) Run(args []string) (string, error) {
	k.runs = append(k.runs, args)
	return k.output, k.err
}

func (k *recordingKubectl) RunInteractive(args []string, quiet bool) (int, error) {
	k.interactive = append(k.interactive, args)
	k.quietStderr = quiet
	return k.exitCode, nil
}

func (k *recordingKubectl) Probe(args []string) int {
	k.probes = append(k.probes, args)
	return k.probeCode
}

func (k *recordingKubectl) CurrentNamespace() string { return "prod" }
func (k *recordingKubectl) CurrentContext() string   { return "test" }

func joinArgs(args []string) string { return strings.Join(args, " ") }

func noStatus(string) func() { return func() {} }

func TestDescribeBuildsKubectlCommand(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := DescribeCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute(1, []string{"--show-events=false"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "describe Pod nginx -n prod --show-events=false"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// A non-zero exit is only worth reporting as stale state if the resource is
// genuinely gone; otherwise kubectl already explained itself.
func TestDescribeProbesOnFailure(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 1}
	err := DescribeCommand{Kubectl: kubectl, State: pod("nginx")}.Execute(1, nil)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a StaleResourceError", err)
	}
	if len(kubectl.probes) != 1 {
		t.Errorf("probes = %d, want 1", len(kubectl.probes))
	}
}

func TestDescribeSucceedingDoesNotProbe(t *testing.T) {
	kubectl := &recordingKubectl{}
	if err := (DescribeCommand{Kubectl: kubectl, State: pod("nginx")}).Execute(1, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kubectl.probes) != 0 {
		t.Errorf("probed %d times on success, want 0", len(kubectl.probes))
	}
}

// A resource that still exists means the failure was something else: not stale
// state, but still a failure. kubectl has already printed why, so the exit code
// is forwarded without a second message.
//
// Returning nil here is what made `kx describe 1 --bogus-flag` print kubectl's
// error and then exit 0, so a shell could not tell it had failed.
func TestDescribeFailureOnLiveResourceForwardsTheExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 3, probeCode: 0}
	err := DescribeCommand{Kubectl: kubectl, State: pod("nginx")}.Execute(1, nil)

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %#v, want SilentError", err)
	}
	if silent.Code != 3 {
		t.Errorf("exit code = %d, want kubectl's 3", silent.Code)
	}
	// Not stale, so the refresh path must leave it alone.
	if isStale(err) {
		t.Error("a live resource's failure was classified as stale")
	}
}

// The same failure against a resource that has gone is stale state, and keeps
// routing into the refresh instead.
func TestDescribeFailureOnVanishedResourceStaysStale(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 1}
	err := DescribeCommand{Kubectl: kubectl, State: pod("nginx")}.Execute(1, nil)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %#v, want StaleResourceError", err)
	}
	if !isStale(err) {
		t.Error("StaleResourceError is not classified as stale")
	}
}

func TestEditAndPortForwardForwardTheExitCode(t *testing.T) {
	t.Run("edit", func(t *testing.T) {
		kubectl := &recordingKubectl{exitCode: 5, probeCode: 0}
		err := EditCommand{Kubectl: kubectl, State: pod("nginx")}.Execute(1, nil)
		var silent SilentError
		if !errors.As(err, &silent) || silent.Code != 5 {
			t.Errorf("err = %#v, want SilentError{5}", err)
		}
	})
	t.Run("port-forward", func(t *testing.T) {
		kubectl := &recordingKubectl{exitCode: 2, probeCode: 0}
		err := PortForwardCommand{Kubectl: kubectl, State: pod("nginx")}.
			Execute(1, "8080:80", nil)
		var silent SilentError
		if !errors.As(err, &silent) || silent.Code != 2 {
			t.Errorf("err = %#v, want SilentError{2}", err)
		}
	})
}

func TestLogsForwardsTheExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 4, probeCode: 0}
	err := LogsCommand{Kubectl: kubectl, State: pod("nginx"), Status: noStatus}.
		Execute(1, nil)
	var silent SilentError
	if !errors.As(err, &silent) || silent.Code != 4 {
		t.Errorf("err = %#v, want SilentError{4}", err)
	}
}

// kubectl's stderr is suppressed for `kx exec -- cmd`, so kx has to report the
// failure itself — but the container's exit code is what a script needs back,
// not a flat 1.
func TestExecForwardsTheContainerExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 42, probeCode: 0}
	err := ExecCommand{Kubectl: kubectl, State: pod("nginx"), Shells: []string{"sh"}}.
		Execute(1, []string{"false"}, nil)

	var exit ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %#v, want ExitError", err)
	}
	if exit.Code != 42 {
		t.Errorf("exit code = %d, want the container's 42", exit.Code)
	}
	if !strings.Contains(exit.Message, "exit 42") {
		t.Errorf("message = %q, want it to name the code", exit.Message)
	}
}

func TestDeleteConfirmsBeforeDeleting(t *testing.T) {
	kubectl := &recordingKubectl{}
	var prompted string
	message, err := DeleteCommand{
		Kubectl: kubectl,
		State:   pod("nginx"),
		Confirm: func(m string) error { prompted = m; return nil },
		Status:  noStatus,
	}.Execute(1, false)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if prompted != "Delete Pod/nginx in prod?" {
		t.Errorf("prompt = %q", prompted)
	}
	if want := "delete Pod nginx -n prod"; joinArgs(kubectl.runs[0]) != want {
		t.Errorf("args = %q, want %q", joinArgs(kubectl.runs[0]), want)
	}
	if message != "Deleted Pod/nginx" {
		t.Errorf("message = %q", message)
	}
}

// Declining the prompt must delete nothing.
func TestDeleteAbortsWithoutConfirmation(t *testing.T) {
	kubectl := &recordingKubectl{}
	_, err := DeleteCommand{
		Kubectl: kubectl,
		State:   pod("nginx"),
		Confirm: func(string) error { return errors.New("aborted") },
		Status:  noStatus,
	}.Execute(1, false)
	if err == nil {
		t.Fatal("Execute succeeded despite an aborted confirmation")
	}
	if len(kubectl.runs) != 0 {
		t.Errorf("kubectl was called %d times after an abort, want 0", len(kubectl.runs))
	}
}

func TestDeleteSkipsPromptWithYes(t *testing.T) {
	kubectl := &recordingKubectl{}
	prompted := false
	_, err := DeleteCommand{
		Kubectl: kubectl,
		State:   pod("nginx"),
		Confirm: func(string) error { prompted = true; return nil },
		Status:  noStatus,
	}.Execute(1, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if prompted {
		t.Error("prompted despite --yes")
	}
}

func TestScaleSupportedKinds(t *testing.T) {
	for _, kind := range []kinds.Kind{kinds.Deployment, kinds.StatefulSet, kinds.ReplicaSet} {
		kubectl := &recordingKubectl{}
		message, err := ScaleCommand{Kubectl: kubectl, State: workload("api", kind)}.Execute(1, 3)
		if err != nil {
			t.Fatalf("Execute(%s): %v", kind, err)
		}
		want := "scale " + string(kind) + "/api --replicas=3 -n prod"
		if got := joinArgs(kubectl.runs[0]); got != want {
			t.Errorf("args = %q, want %q", got, want)
		}
		if !strings.Contains(message, "3 replicas") {
			t.Errorf("message = %q, want a plural replica count", message)
		}
	}
}

func TestScaleSingularReplica(t *testing.T) {
	message, err := ScaleCommand{
		Kubectl: &recordingKubectl{}, State: workload("api", kinds.Deployment),
	}.Execute(1, 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasSuffix(message, "1 replica") {
		t.Errorf("message = %q, want a singular replica", message)
	}
}

func TestScaleRejectsUnsupportedKind(t *testing.T) {
	kubectl := &recordingKubectl{}
	_, err := ScaleCommand{Kubectl: kubectl, State: pod("nginx")}.Execute(1, 3)
	if err == nil {
		t.Fatal("scaled a Pod, want an error")
	}
	if len(kubectl.runs) != 0 {
		t.Error("kubectl was called for an unsupported kind")
	}
}

// `rollout status` blocks until the rollout settles, so it must stream rather
// than be captured.
func TestRolloutStatusStreams(t *testing.T) {
	kubectl := &recordingKubectl{}
	output, err := RolloutCommand{Kubectl: kubectl, State: workload("api", kinds.Deployment)}.
		Execute("status", 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if output != "" {
		t.Errorf("output = %q, want it streamed rather than captured", output)
	}
	if len(kubectl.interactive) != 1 || len(kubectl.runs) != 0 {
		t.Errorf("status did not stream: interactive=%d runs=%d",
			len(kubectl.interactive), len(kubectl.runs))
	}
}

func TestRolloutNonStatusIsCaptured(t *testing.T) {
	kubectl := &recordingKubectl{output: "deployment.apps/api restarted\n"}
	output, err := RolloutCommand{Kubectl: kubectl, State: workload("api", kinds.Deployment)}.
		Execute("restart", 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(output, "restarted") {
		t.Errorf("output = %q, want kubectl's message", output)
	}
	want := "rollout restart Deployment/api -n prod"
	if got := joinArgs(kubectl.runs[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestRolloutRejectsUnknownAction(t *testing.T) {
	_, err := RolloutCommand{Kubectl: &recordingKubectl{}, State: workload("api", kinds.Deployment)}.
		Execute("explode", 1)
	if err == nil {
		t.Fatal("accepted an unknown rollout action")
	}
}

func TestRolloutRejectsUnsupportedKind(t *testing.T) {
	_, err := RolloutCommand{Kubectl: &recordingKubectl{}, State: pod("nginx")}.Execute("restart", 1)
	if err == nil {
		t.Fatal("rolled out a Pod, want an error")
	}
}

func TestPortForwardRejectsUnsupportedKind(t *testing.T) {
	err := PortForwardCommand{
		Kubectl: &recordingKubectl{}, State: workload("cm", kinds.ConfigMap),
	}.Execute(1, "8080:80", nil)
	if err == nil {
		t.Fatal("port-forwarded a ConfigMap, want an error")
	}
}

func TestPortForwardBuildsCommand(t *testing.T) {
	kubectl := &recordingKubectl{}
	if err := (PortForwardCommand{Kubectl: kubectl, State: pod("nginx")}).
		Execute(1, "8080:80", []string{"--address=0.0.0.0"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "port-forward Pod/nginx 8080:80 -n prod --address=0.0.0.0"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestLogsForPodReadsDirectly(t *testing.T) {
	kubectl := &recordingKubectl{}
	if err := (LogsCommand{Kubectl: kubectl, State: pod("nginx"), Status: noStatus}).
		Execute(1, []string{"-f"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if want := "logs nginx -n prod -f"; joinArgs(kubectl.interactive[0]) != want {
		t.Errorf("args = %q, want %q", joinArgs(kubectl.interactive[0]), want)
	}
}

// A workload's logs are aggregated across the pods it owns, via the selector
// read from its spec.
func TestLogsForDeploymentAggregatesBySelector(t *testing.T) {
	kubectl := &recordingKubectl{
		output: `{"spec":{"selector":{"matchLabels":{"app":"web","tier":"api"}}}}`,
	}
	if err := (LogsCommand{
		Kubectl: kubectl, State: workload("web", kinds.Deployment), Status: noStatus,
	}).Execute(1, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "logs -l app=web,tier=api --prefix=true -n prod"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// A Service selects pods directly rather than through a template.
func TestLogsForServiceUsesFlatSelector(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"spec":{"selector":{"app":"web"}}}`}
	if err := (LogsCommand{
		Kubectl: kubectl, State: workload("web", kinds.Service), Status: noStatus,
	}).Execute(1, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(joinArgs(kubectl.interactive[0]), "-l app=web") {
		t.Errorf("args = %q, want the flat selector", joinArgs(kubectl.interactive[0]))
	}
}

func TestLogsForWorkloadWithoutSelectorFails(t *testing.T) {
	kubectl := &recordingKubectl{output: `{"spec":{}}`}
	err := LogsCommand{
		Kubectl: kubectl, State: workload("web", kinds.Deployment), Status: noStatus,
	}.Execute(1, nil)
	if err == nil {
		t.Fatal("aggregated logs with no selector, want an error")
	}
}

func TestLogsRejectsUnsupportedKind(t *testing.T) {
	err := LogsCommand{
		Kubectl: &recordingKubectl{}, State: workload("cm", kinds.ConfigMap), Status: noStatus,
	}.Execute(1, nil)
	if err == nil {
		t.Fatal("read logs from a ConfigMap, want an error")
	}
}

func TestExecRejectsNonPod(t *testing.T) {
	err := ExecCommand{
		Kubectl: &recordingKubectl{}, State: workload("api", kinds.Deployment), Shells: []string{"sh"},
	}.Execute(1, nil, nil)
	if err == nil {
		t.Fatal("exec'd into a Deployment, want an error")
	}
}

func TestExecWithExplicitCommand(t *testing.T) {
	kubectl := &recordingKubectl{}
	if err := (ExecCommand{Kubectl: kubectl, State: pod("nginx"), Shells: []string{"sh"}}).
		Execute(1, []string{"ls", "/app"}, []string{"-c", "sidecar"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "exec -it nginx -n prod -c sidecar -- ls /app"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	// kubectl's "command terminated with exit code N" would be noise on top of
	// whatever the command itself printed.
	if !kubectl.quietStderr {
		t.Error("kubectl stderr was not suppressed for an explicit command")
	}
}

// With no command, each configured shell is probed in order and the first that
// works is used.
func TestExecProbesShellsInOrder(t *testing.T) {
	kubectl := &recordingKubectl{probeCode: 0}
	if err := (ExecCommand{
		Kubectl: kubectl, State: pod("nginx"), Shells: []string{"bash", "sh"},
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(kubectl.probes) != 1 {
		t.Fatalf("probes = %d, want 1 (the first shell worked)", len(kubectl.probes))
	}
	if !strings.HasSuffix(joinArgs(kubectl.probes[0]), "-- bash -c exit 0") {
		t.Errorf("probe = %q, want bash first", joinArgs(kubectl.probes[0]))
	}
	if !strings.HasSuffix(joinArgs(kubectl.interactive[0]), "-- bash") {
		t.Errorf("exec = %q, want the probed shell", joinArgs(kubectl.interactive[0]))
	}
}

func TestExecFailsWhenNoShellFound(t *testing.T) {
	kubectl := &recordingKubectl{probeCode: 1}
	err := ExecCommand{
		Kubectl: kubectl, State: pod("nginx"), Shells: []string{"bash", "sh"},
	}.Execute(1, nil, nil)
	if err == nil {
		t.Fatal("Execute succeeded with no shell available")
	}
	// Both shells are tried, then a final probe checks whether the pod itself
	// is still there before blaming the missing shell.
	shellProbes := 0
	for _, probe := range kubectl.probes {
		if strings.Contains(joinArgs(probe), "-c exit 0") {
			shellProbes++
		}
	}
	if shellProbes != 2 {
		t.Errorf("shell probes = %d, want both shells tried", shellProbes)
	}
}

// listings answers FieldsExpecting from a single current listing, the way the
// state service does: the index counts against that listing, and the kind has
// to match what the command asked for.
type listings struct {
	kind  kinds.Kind
	names []string
}

func (l listings) FieldsExpecting(index int, expected kinds.Kind) (string, string, error) {
	if len(l.names) == 0 {
		return "", "", fmt.Errorf(
			"No state found — run 'kx get %s' to list them first.", expected)
	}
	if index < 1 || index > len(l.names) {
		return "", "", fmt.Errorf(
			"Index %d is out of range — the current listing has %d %s. Run 'kx get %s' to relist.",
			index, len(l.names), l.kind, expected)
	}
	if l.kind != expected {
		return "", "", fmt.Errorf("Index %d is %s/%s, not %s — run 'kx get %s' to relist.",
			index, l.kind, l.names[index-1], expected, expected)
	}
	return l.names[index-1], "prod", nil
}

func namespaces(names ...string) listings {
	return listings{kind: kinds.Namespace, names: names}
}

// kubectl config set-context accepts any string, so a stale index pointing at a
// Pod would otherwise make that pod's name the active namespace.
func TestNamespaceSwitchRejectsWrongKind(t *testing.T) {
	kubectl := &recordingKubectl{}
	state := listings{kind: kinds.Pod, names: []string{"nginx"}}

	_, err := SwitchCommand{Kubectl: kubectl, State: state}.namespace(1)
	if err == nil {
		t.Fatal("switched to a Pod as a namespace, want an error")
	}
	if !strings.Contains(err.Error(), "not Namespace") {
		t.Errorf("err = %v, want a kind mismatch", err)
	}
	if len(kubectl.runs) != 0 {
		t.Error("kubectl config was called for a wrong-kind index")
	}
}

func TestNamespaceSwitch(t *testing.T) {
	kubectl := &recordingKubectl{}
	name, err := SwitchCommand{Kubectl: kubectl, State: namespaces("staging")}.namespace(1)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if name != "staging" {
		t.Errorf("name = %q, want staging", name)
	}
	want := "config set-context --current --namespace=staging"
	if got := joinArgs(kubectl.runs[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// Setting a namespace is a local kubeconfig edit that kubectl does not validate
// — pointing at one before creating it is a normal thing to do — and every
// staleness check in kx reacts to a failure rather than pre-empting one. So the
// switch spends no round trip checking the namespace exists.
func TestNamespaceSwitchDoesNotProbeTheCluster(t *testing.T) {
	kubectl := &recordingKubectl{}
	if _, err := (SwitchCommand{Kubectl: kubectl, State: namespaces("staging")}).
		namespace(1); err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if len(kubectl.probes) != 0 {
		t.Errorf("namespace switch probed the cluster: %v", kubectl.probes)
	}
}

func TestContextSwitchWithoutAContextListing(t *testing.T) {
	state := listings{kind: kinds.Pod, names: []string{"nginx"}}
	_, err := SwitchCommand{Kubectl: &recordingKubectl{}, State: state}.context(1)
	if err == nil {
		t.Fatal("switched to a context with no context listing, want an error")
	}
}

func TestContextSwitch(t *testing.T) {
	kubectl := &recordingKubectl{}
	state := listings{kind: ContextKind, names: []string{"docker-desktop"}}
	name, err := SwitchCommand{Kubectl: kubectl, State: state}.context(1)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if name != "docker-desktop" {
		t.Errorf("name = %q", name)
	}
	if want := "config use-context docker-desktop"; joinArgs(kubectl.runs[0]) != want {
		t.Errorf("args = %q, want %q", joinArgs(kubectl.runs[0]), want)
	}
	// use-context rejects an unknown name itself, so kx spends no round trip
	// checking what kubectl is about to check.
	if len(kubectl.probes) != 0 {
		t.Errorf("context switch probed the cluster: %v", kubectl.probes)
	}
}

// Contexts are tagged with a pseudo-kind so a context index can be told apart
// from a resource index.
func TestContextsSavesWithContextKind(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop",
	}
	states := &fakeState{}
	if _, err := (ContextsCommand{Kubectl: kubectl, State: states, Index: indexService()}).
		Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 1 {
		t.Fatalf("saved %d entries, want 1", len(states.saved))
	}
	if kind, _ := states.saved[0].Resources.Kind("docker-desktop"); kind != ContextKind {
		t.Errorf("kind = %q, want %q", kind, ContextKind)
	}
}
