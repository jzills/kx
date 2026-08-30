package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
)

// fakeResolver resolves indexes without touching a state file.
type fakeResolver struct {
	name      string
	namespace string
	kind      kinds.Kind
	err       error
	// count and countErr back Count(), used by tests that expand an
	// open-ended range ("5..") against a fixed listing size.
	count    int
	countErr error
}

func (f fakeResolver) Fields(int) (string, string, kinds.Kind, error) {
	if f.err != nil {
		return "", "", "", f.err
	}
	return f.name, f.namespace, f.kind, nil
}

func (f fakeResolver) Count() (int, error) {
	return f.count, f.countErr
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
	// contextReads counts CurrentContext calls, each of which is a kubectl
	// subprocess in the real service.
	contextReads int
	// namespace overrides what CurrentNamespace reports, so a test can move the
	// caller after a listing was taken. Empty keeps the default.
	namespace string
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

func (k *recordingKubectl) Watch(args []string, onLine func(string) error) error {
	k.runs = append(k.runs, args)
	return k.err
}

func (k *recordingKubectl) CurrentNamespace() string {
	if k.namespace != "" {
		return k.namespace
	}
	return "prod"
}

func (k *recordingKubectl) CurrentContext() string {
	k.contextReads++
	return "test"
}

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

func TestCopyResolvesAnIndexedSource(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("1:/var/log/app.log", "./app.log", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "cp prod/nginx:/var/log/app.log ./app.log"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCopyResolvesAnIndexedDestination(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("./patch.conf", "1:/etc/app/patch.conf", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "cp ./patch.conf prod/nginx:/etc/app/patch.conf"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// Neither side has to be indexed — a fully passthrough invocation (e.g. an
// already-qualified ns/pod:path) is not kx's business to validate.
func TestCopyPassesThroughArgsWithNoIndex(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("staging/other-pod:/etc/foo", "./foo", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "cp staging/other-pod:/etc/foo ./foo"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCopyForwardsExtraArgs(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("1:/etc/foo", "./foo", []string{"-c", "sidecar"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "cp prod/nginx:/etc/foo ./foo -c sidecar"
	if got := joinArgs(kubectl.interactive[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestCopyRejectsAnIndexedNonPod(t *testing.T) {
	kubectl := &recordingKubectl{}
	err := CopyCommand{Kubectl: kubectl, State: workload("api", kinds.Deployment)}.
		Execute("1:/etc/foo", "./foo", nil)
	if err == nil {
		t.Fatal("copied from a Deployment index, want an error")
	}
	if err.Error() != "kx cp does not support 'Deployment' — only Pods." {
		t.Errorf("err = %q, want it to name Deployment and Pods", err.Error())
	}
	if len(kubectl.interactive) != 0 {
		t.Error("kubectl was called for a rejected kind")
	}
}

// A vanished indexed pod is stale state, exactly like exec/port-forward.
func TestCopyFailureOnVanishedPodStaysStale(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 1}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("1:/etc/foo", "./foo", nil)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %#v, want StaleResourceError", err)
	}
}

// A live pod's own failure (e.g. tar missing in the container) is not
// stale state; kubectl already printed why, so the exit code is forwarded
// without a second message — same contract as describe/edit/port-forward.
func TestCopyFailureOnLivePodForwardsTheExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 3, probeCode: 0}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("1:/etc/foo", "./foo", nil)

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %#v, want SilentError", err)
	}
	if silent.Code != 3 {
		t.Errorf("code = %d, want kubectl's 3", silent.Code)
	}
}

// Neither side indexed: nothing to probe for staleness, so a failure is a
// bare exit-code forward — same shape LogsCommand's aggregate branch uses.
func TestCopyFailureWithNoIndexIsABareExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 5}
	err := CopyCommand{Kubectl: kubectl, State: pod("nginx")}.
		Execute("staging/other-pod:/etc/foo", "./foo", nil)

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %#v, want SilentError", err)
	}
	if silent.Code != 5 {
		t.Errorf("code = %d, want 5", silent.Code)
	}
	if len(kubectl.probes) != 0 {
		t.Error("probed for staleness with nothing indexed to check")
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

// A rollout status that fails must not be reported as a success: before this
// fix, RolloutCommand discarded RunInteractive's exit code entirely.
func TestRolloutStatusProbesOnFailure(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 1}
	_, err := RolloutCommand{Kubectl: kubectl, State: workload("api", kinds.Deployment)}.
		Execute("status", 1)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want a StaleResourceError", err)
	}
	if len(kubectl.probes) != 1 {
		t.Errorf("probes = %d, want 1", len(kubectl.probes))
	}
}

func TestRolloutStatusFailureOnLiveWorkloadForwardsTheExitCode(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 3, probeCode: 0}
	_, err := RolloutCommand{Kubectl: kubectl, State: workload("api", kinds.Deployment)}.
		Execute("status", 1)

	var silent SilentError
	if !errors.As(err, &silent) {
		t.Fatalf("err = %#v, want SilentError", err)
	}
	if silent.Code != 3 {
		t.Errorf("code = %d, want 3", silent.Code)
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

// Message casing matches its four siblings ("scale/rollout/port-forward/scan
// is not supported for '%s'.") rather than standing out as the one
// capitalized "Logs are not supported...".
func TestLogsRejectsUnsupportedKind(t *testing.T) {
	err := LogsCommand{
		Kubectl: &recordingKubectl{}, State: workload("cm", kinds.ConfigMap), Status: noStatus,
	}.Execute(1, nil)
	want := "kx logs does not support 'ConfigMap' — only Pods, Deployments, " +
		"StatefulSets, DaemonSets and Services."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
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

// slots answers FieldsNamed from per-kind slots, the way the state service
// does: the index counts against that kind's own listing, and a kind with no
// listing has nothing to resolve against.
type slots map[kinds.Kind][]string

func (s slots) FieldsNamed(index int, kind kinds.Kind) (string, string, error) {
	names, ok := s[kind]
	if !ok {
		return "", "", fmt.Errorf(
			"No %s listing yet — run 'kx %s' to list them.", kind, strings.ToLower(string(kind)))
	}
	if index < 1 || index > len(names) {
		return "", "", fmt.Errorf(
			"Index %d is out of range — the last %s listing had %d.", index, kind, len(names))
	}
	return names[index-1], "prod", nil
}

func namespaceSlot(names ...string) slots {
	return slots{kinds.Namespace: names}
}

// The sequence from #156. Whatever is in history — here a pods listing — the
// index counts against the namespace slot, because `kx ns 2` already said which
// kind it means.
func TestNamespaceSwitchReadsTheSlotNotTheCurrentListing(t *testing.T) {
	kubectl := &recordingKubectl{}
	state := slots{
		kinds.Namespace: {"default", "staging"},
		kinds.Pod:       {"nginx", "redis"},
	}

	name, err := SwitchCommand{Kubectl: kubectl, State: state}.namespace(2)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if name != "staging" {
		t.Errorf("name = %q, want staging — index 2 resolved against the wrong listing", name)
	}
	want := "config set-context --current --namespace=staging"
	if got := joinArgs(kubectl.runs[0]); got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// An unpopulated slot must not fall back to the current listing: that fallback
// is what let a pod's name become the active namespace, and kubectl config
// set-context validates nothing that would catch it.
func TestNamespaceSwitchWithoutANamespaceListing(t *testing.T) {
	kubectl := &recordingKubectl{}
	state := slots{kinds.Pod: {"nginx"}}

	_, err := SwitchCommand{Kubectl: kubectl, State: state}.namespace(1)
	if err == nil {
		t.Fatal("switched with no namespace listing, want an error")
	}
	if !strings.Contains(err.Error(), "No Namespace listing") {
		t.Errorf("err = %v, want it to report an empty slot", err)
	}
	if len(kubectl.runs) != 0 {
		t.Error("kubectl config was called with no namespace listing")
	}
}

func TestNamespaceSwitch(t *testing.T) {
	kubectl := &recordingKubectl{}
	name, err := SwitchCommand{Kubectl: kubectl, State: namespaceSlot("staging")}.namespace(1)
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
	if _, err := (SwitchCommand{Kubectl: kubectl, State: namespaceSlot("staging")}).
		namespace(1); err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if len(kubectl.probes) != 0 {
		t.Errorf("namespace switch probed the cluster: %v", kubectl.probes)
	}
}

func TestContextSwitchWithoutAContextListing(t *testing.T) {
	state := slots{kinds.Pod: {"nginx"}}
	_, err := SwitchCommand{Kubectl: &recordingKubectl{}, State: state}.context(1)
	if err == nil {
		t.Fatal("switched to a context with no context listing, want an error")
	}
}

func TestContextSwitch(t *testing.T) {
	kubectl := &recordingKubectl{}
	state := slots{kinds.Context: {"docker-desktop"}}
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
	if _, _, err := (ContextsCommand{Kubectl: kubectl, State: states, Index: indexService()}).
		Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.named) != 1 {
		t.Fatalf("saved %d slot entries, want 1", len(states.named))
	}
	if kind, _ := states.named[0].Resources.Kind("docker-desktop"); kind != kinds.Context {
		t.Errorf("kind = %q, want %q", kind, kinds.Context)
	}
}

// A context is a kubeconfig entry, not a server resource — there is no
// `kubectl describe context` — so nothing downstream can consume one from the
// history stack, and putting it there only evicts work.
func TestContextsDoesNotTouchHistory(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop",
	}
	states := &fakeState{}
	if _, _, err := (ContextsCommand{Kubectl: kubectl, State: states, Index: indexService()}).
		Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(states.saved) != 0 {
		t.Errorf("pushed %d entries onto history, want 0", len(states.saved))
	}
}

// The listing is saved with the active context already, so Execute hands it
// back for the caption rather than making the caller fetch it.
func TestContextsReturnsTheActiveContext(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop",
	}
	_, current, err := ContextsCommand{
		Kubectl: kubectl, State: &fakeState{}, Index: indexService(),
	}.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if current != "test" {
		t.Errorf("context = %q, want the active context", current)
	}
}

// kubectl marks the active context in a CURRENT column that is blank on every
// other row. That blank is kept now: Add hands the renderer its parsed rows, so
// nothing has to survive a second trip through padded text where an empty cell
// and column padding are the same run of spaces.
//
// This column was dropped entirely while the renderer still re-parsed, because
// Add prepends the index column and made the blank cell interior, which no
// parser can recover. Its return is the proof that the round-trip is gone.
func TestContextsKeepsTheCurrentMarkerColumn(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER          NAMESPACE\n" +
			"          alt              docker-desktop   default\n" +
			"*         docker-desktop   docker-desktop   diagnostics",
	}
	states := &fakeState{}

	table, _, err := ContextsCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if index.ColumnIndex(table.Headers, "CURRENT") < 0 {
		t.Fatalf("table lost the CURRENT column: %q", table.Headers)
	}
	if len(table.Rows) != 2 {
		t.Fatalf("rows = %d, want 2: %q", len(table.Rows), table.Rows)
	}
	// The blank marker on the non-current row is the cell that used to vanish,
	// taking every value after it one column to the left.
	nameIdx := index.ColumnIndex(table.Headers, "NAME")
	currentIdx := index.ColumnIndex(table.Headers, "CURRENT")
	if table.Rows[0][currentIdx] != "" || table.Rows[0][nameIdx] != "alt" {
		t.Errorf("row 0 = %q, want a blank CURRENT and NAME alt", table.Rows[0])
	}
	if table.Rows[1][currentIdx] != "*" || table.Rows[1][nameIdx] != "docker-desktop" {
		t.Errorf("row 1 = %q, want CURRENT '*' and NAME docker-desktop", table.Rows[1])
	}
}

// Dropping a column must not lose the rest of the row.
func TestContextsKeepsTheRemainingColumns(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER          NAMESPACE\n" +
			"          alt              docker-desktop   default\n" +
			"*         docker-desktop   docker-desktop   diagnostics",
	}

	table, _, err := ContextsCommand{
		Kubectl: kubectl, State: &fakeState{}, Index: indexService(),
	}.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Compared as parsed headers, not as substrings: `strings.Contains(table,
	// "NAME")` is satisfied by "NAMESPACE", so a version that dropped NAME
	// passed that check.
	want := []string{"X", "CURRENT", "NAME", "CLUSTER", "NAMESPACE"}
	if len(table.Headers) != len(want) {
		t.Fatalf("headers = %v, want %v", table.Headers, want)
	}
	for i := range want {
		if table.Headers[i] != want[i] {
			t.Errorf("headers[%d] = %q, want %q (full: %v)",
				i, table.Headers[i], want[i], table.Headers)
		}
	}
	if len(table.Rows) != 2 || table.Rows[0][4] != "default" || table.Rows[1][4] != "diagnostics" {
		t.Errorf("rows lost their trailing values: %v", table.Rows)
	}
}

// Both contexts must reach the slot, or `kx context <n>` cannot select the one
// that isn't active — the failure that started this.
func TestContextsIndexesEveryContext(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER          NAMESPACE\n" +
			"          alt              docker-desktop   default\n" +
			"*         docker-desktop   docker-desktop   diagnostics",
	}
	states := &fakeState{}

	if _, _, err := (ContextsCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}).Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(states.named) != 1 {
		t.Fatalf("saved %d slot entries, want 1", len(states.named))
	}
	names := states.named[0].Resources.Names()
	if len(names) != 2 || names[0] != "alt" || names[1] != "docker-desktop" {
		t.Errorf("slot names = %v, want [alt docker-desktop]", names)
	}
}

// `kubectl config get-contexts` prints a NAMESPACE column, but it names the
// namespace each context *defaults to* — not one the context lives in, because
// contexts are kubeconfig entries and are not namespaced at all.
//
// Recorded as a resource namespace it was read as one, back when the scope was
// inferred from whether resources carried namespaces — which captioned the
// context slot in `kx state --targets` as "all namespaces".
func TestContextsRecordsNoResourceNamespace(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER          NAMESPACE\n" +
			"          alt              docker-desktop   default\n" +
			"*         docker-desktop   docker-desktop   diagnostics",
	}
	states := &fakeState{}

	if _, _, err := (ContextsCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}).Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(states.named) != 1 {
		t.Fatalf("saved %d slot entries, want 1", len(states.named))
	}
	for _, resource := range states.named[0].Resources.Entries() {
		if resource.Namespace != "" {
			t.Errorf("context %q recorded namespace %q, want none",
				resource.Name, resource.Namespace)
		}
	}
	if states.named[0].AllNamespaces {
		t.Error("context slot reports itself as spanning namespaces")
	}
}

// The entry namespace is a scope, and a contexts listing has none. The active
// context is already recorded in Context — stamped there by the state service —
// so writing it here as well captioned `kx state --targets` with it twice.
func TestContextsRecordsNoEntryNamespace(t *testing.T) {
	kubectl := &recordingKubectl{
		output: "CURRENT   NAME             CLUSTER\n*         docker-desktop   docker-desktop",
	}
	states := &fakeState{}

	if _, _, err := (ContextsCommand{
		Kubectl: kubectl, State: states, Index: indexService(),
	}).Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(states.named) != 1 {
		t.Fatalf("saved %d slot entries, want 1", len(states.named))
	}
	if ns := states.named[0].Namespace; ns != "" {
		t.Errorf("slot namespace = %q, want none — a context is not a scope", ns)
	}
}

// kx debug attaches a container that has a shell to a pod whose own image has
// none — the case kx exec dead-ends on with "No shell found in container".
func TestDebugAttachesAnEphemeralContainer(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "metrics-server", namespace: "kube-system", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := joinArgs(kubectl.interactive[0])
	for _, want := range []string{"debug", "-it", "metrics-server", "-n kube-system", "--image=busybox"} {
		if !strings.Contains(got, want) {
			t.Errorf("args = %q, want them to contain %q", got, want)
		}
	}
}

// A Pod takes an ephemeral container and a Node takes a debug pod of its own.
// Nothing else is either, so nothing else is debuggable.
func TestDebugRejectsNonPods(t *testing.T) {
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Deployment}

	err := DebugCommand{
		Kubectl: &recordingKubectl{}, State: resolver, Image: "busybox",
	}.Execute(1, nil, nil)

	if err == nil {
		t.Fatal("debug on a Deployment succeeded")
	}
	if !strings.Contains(err.Error(), "'Deployment'") ||
		!strings.Contains(err.Error(), "only Pods and Nodes") {
		t.Errorf("err = %q, want it to name Deployment, Pods and Nodes", err)
	}
}

// An explicit --image is the user overriding their own default for one run, so
// kx must not also pass the configured one and leave kubectl with two.
func TestDebugExplicitImageWins(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, []string{"--image=alpine:3.20"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := joinArgs(kubectl.interactive[0])
	if strings.Contains(got, "busybox") {
		t.Errorf("args = %q, want the configured image dropped", got)
	}
	if !strings.Contains(got, "--image=alpine:3.20") {
		t.Errorf("args = %q, want the explicit image", got)
	}
}

// Everything after -- is the command to run inside the debug container.
func TestDebugPassesTheCommandThrough(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, []string{"ls", "/proc/1/root"}, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := joinArgs(kubectl.interactive[0]); !strings.HasSuffix(got, "-- ls /proc/1/root") {
		t.Errorf("args = %q, want the command last, after --", got)
	}
}

// Without --target the debug container joins the pod but not the target's
// process namespace, putting /proc/1/root — the filesystem you came for on an
// image with no shell — out of reach. One container is unambiguous, so kx
// supplies it.
func TestDebugTargetsTheOnlyContainer(t *testing.T) {
	kubectl := &recordingKubectl{output: "metrics-server\n"}
	resolver := fakeResolver{name: "metrics-server", namespace: "kube-system", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := joinArgs(kubectl.interactive[0]); !strings.Contains(got, "--target=metrics-server") {
		t.Errorf("args = %q, want the sole container targeted", got)
	}
}

// Several containers have no single answer, and kubectl's own error names the
// candidates better than a guess would.
func TestDebugDoesNotGuessAmongSeveralContainers(t *testing.T) {
	kubectl := &recordingKubectl{output: "app\nsidecar\n"}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := joinArgs(kubectl.interactive[0]); strings.Contains(got, "--target") {
		t.Errorf("args = %q, want no target guessed for a multi-container pod", got)
	}
}

// An explicit --target is the user's answer; kx must not add a second.
func TestDebugKeepsAnExplicitTarget(t *testing.T) {
	kubectl := &recordingKubectl{output: "app\n"}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, []string{"--target=sidecar"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := joinArgs(kubectl.interactive[0])
	if strings.Count(got, "--target") != 1 {
		t.Errorf("args = %q, want exactly one --target", got)
	}
	if !strings.Contains(got, "--target=sidecar") {
		t.Errorf("args = %q, want the explicit target kept", got)
	}
}

// The lookup is a convenience, not a precondition: a cluster that will not
// answer it still gets a debug session, just without the shared namespace.
//
// The fake answers with output *and* an error, which is how kubectl actually
// fails — it prints what it managed before reporting the failure. A fake that
// returned an error with empty output would pass this test whether or not the
// error was checked at all, since both paths end with no containers.
func TestDebugRunsWhenTheContainerLookupFails(t *testing.T) {
	kubectl := &recordingKubectl{output: "stale\n", err: errors.New("connection refused")}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(kubectl.interactive) != 1 {
		t.Fatalf("debug did not run: %v", kubectl.interactive)
	}
	if got := joinArgs(kubectl.interactive[0]); strings.Contains(got, "--target") {
		t.Errorf("args = %q, want no target read out of a failed lookup", got)
	}
}

// A value-less --image is what a shell expanding to nothing produces. With
// extractString's error discarded it read as "no image given", so kx appended
// its own --image=busybox and forwarded the bare --image alongside it, leaving
// kubectl to complain about a flag the user could see they had written.
func TestDebugReportsAMalformedImageFlag(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, []string{"--image"})

	if err == nil {
		t.Fatal("a value-less --image was accepted")
	}
	if !strings.Contains(err.Error(), "--image") {
		t.Errorf("error = %q, want it to name the flag", err)
	}
	if len(kubectl.interactive) != 0 {
		t.Errorf("ran kubectl anyway: %v", kubectl.interactive)
	}
}

// --target is read by hand for the same reason and was discarding the same
// error, so a value-less one reached kubectl beside a --target kx had guessed.
func TestDebugReportsAMalformedTargetFlag(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "api", namespace: "prod", kind: kinds.Pod}

	err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, []string{"--target"})

	if err == nil {
		t.Fatal("a value-less --target was accepted")
	}
	if !strings.Contains(err.Error(), "--target") {
		t.Errorf("error = %q, want it to name the flag", err)
	}
	if len(kubectl.interactive) != 0 {
		t.Errorf("ran kubectl anyway: %v", kubectl.interactive)
	}
}

// kx logs aggregates across a workload's pods and kx port-forward takes one
// directly, but kx exec alone answered "only supported for pods" — so the one
// command people reach for most needed an index kx had just made harder to
// get. kubectl resolves TYPE/NAME to a pod itself, the same way port-forward
// already relies on.
func TestExecAcceptsAWorkload(t *testing.T) {
	for _, kind := range []kinds.Kind{
		kinds.Deployment, kinds.ReplicaSet, kinds.StatefulSet, kinds.DaemonSet,
	} {
		kubectl := &recordingKubectl{probeCode: 0}
		err := ExecCommand{
			Kubectl: kubectl, State: workload("web", kind), Shells: []string{"sh"},
		}.Execute(1, []string{"ls"}, nil)
		if err != nil {
			t.Fatalf("%s: Execute: %v", kind, err)
		}
		want := string(kind) + "/web"
		last := kubectl.interactive[len(kubectl.interactive)-1]
		if !slices.Contains(last, want) {
			t.Errorf("%s: kubectl args = %v, want the target spelled %q", kind, last, want)
		}
	}
}

// A Pod is still addressed by bare name, not Pod/name. Both work in kubectl,
// but the bare form is what every other pod-only command already sends and
// what the existing tests pin.
func TestExecStillAddressesAPodByName(t *testing.T) {
	kubectl := &recordingKubectl{probeCode: 0}
	if err := (ExecCommand{
		Kubectl: kubectl, State: pod("nginx"), Shells: []string{"sh"},
	}).Execute(1, []string{"ls"}, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	args := kubectl.interactive[len(kubectl.interactive)-1]
	if slices.Contains(args, "Pod/nginx") {
		t.Errorf("kubectl args = %v, want the bare pod name", args)
	}
	if !slices.Contains(args, "nginx") {
		t.Errorf("kubectl args = %v, want the pod named", args)
	}
}

// The shell probe has to name the same target the session will, or kx probes
// one thing and execs into another.
func TestExecProbesTheSameTargetItExecsInto(t *testing.T) {
	kubectl := &recordingKubectl{probeCode: 0}
	if err := (ExecCommand{
		Kubectl: kubectl, State: workload("web", kinds.Deployment), Shells: []string{"sh"},
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, call := range append(append([][]string{}, kubectl.probes...), kubectl.interactive...) {
		if !slices.Contains(call, "Deployment/web") {
			t.Errorf("call %v does not name Deployment/web", call)
		}
	}
}

// Service is not in the allowlist: kubectl exec does not accept one, so kx
// refuses it here rather than letting kubectl produce a worse message.
func TestExecStillRefusesAKindKubectlCannotExecInto(t *testing.T) {
	for _, kind := range []kinds.Kind{kinds.Service, kinds.ConfigMap, kinds.Node} {
		err := ExecCommand{
			Kubectl: &recordingKubectl{}, State: workload("thing", kind), Shells: []string{"sh"},
		}.Execute(1, []string{"ls"}, nil)
		if err == nil {
			t.Errorf("%s: Execute succeeded, want a refusal", kind)
			continue
		}
		if !strings.Contains(err.Error(), "kx exec does not support") ||
			!strings.Contains(err.Error(), string(kind)) {
			t.Errorf("%s: err = %q, want an unsupported-kind message naming it", kind, err)
		}
	}
}

// The staleness check has to ask about the resolved kind. Asked about a Pod
// that never existed under that name, a vanished Deployment reported the wrong
// thing — or nothing.
func TestExecChecksStalenessAgainstTheResolvedKind(t *testing.T) {
	kubectl := &recordingKubectl{exitCode: 1, probeCode: 1}
	err := ExecCommand{
		Kubectl: kubectl, State: workload("web", kinds.Deployment), Shells: []string{"sh"},
	}.Execute(1, []string{"ls"}, nil)

	var stale StaleResourceError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %#v, want StaleResourceError", err)
	}
	if stale.Kind != kinds.Deployment {
		t.Errorf("stale kind = %q, want Deployment", stale.Kind)
	}
}

// Every "wrong kind for this command" refusal names two things: the kind the
// index actually resolved to, and the kinds the command does work on.
//
// The codebase had each half without the other — "scale is not supported for
// 'Pod'." named the first, "cp is only supported for pods." named the second —
// so which fact you got depended on which command you happened to type. Driven
// over the real commands rather than over unsupportedKindError, which would
// only prove the helper agrees with itself.
func TestUnsupportedKindMessagesNameBothTheKindAndTheSupportedKinds(t *testing.T) {
	const wrong = kinds.ConfigMap
	refusals := map[string]func() error{
		"scale": func() error {
			_, err := ScaleCommand{State: workload("cm", wrong)}.Execute(1, 2)
			return err
		},
		"rollout": func() error {
			_, err := RolloutCommand{State: workload("cm", wrong)}.Execute("status", 1)
			return err
		},
		"port-forward": func() error {
			return PortForwardCommand{State: workload("cm", wrong)}.Execute(1, "80", nil)
		},
		"exec": func() error {
			return ExecCommand{State: workload("cm", wrong)}.Execute(1, nil, nil)
		},
		"logs": func() error {
			return LogsCommand{
				Kubectl: &recordingKubectl{}, State: workload("cm", wrong), Status: noStatus,
			}.Execute(1, nil)
		},
		"scan": func() error {
			_, err := ScanCommand{State: workload("cm", wrong), Status: noStatus}.Execute(1, "trivy")
			return err
		},
	}

	for command, refuse := range refusals {
		t.Run(command, func(t *testing.T) {
			err := refuse()
			if err == nil {
				t.Fatalf("kx %s accepted a %s", command, wrong)
			}
			message := err.Error()
			if !strings.Contains(message, "'"+string(wrong)+"'") {
				t.Errorf("err = %q, want it to name the kind the index resolved to", message)
			}
			if !strings.Contains(message, " — only ") {
				t.Errorf("err = %q, want it to name the kinds %s does support", message, command)
			}
			if !strings.HasSuffix(message, ".") {
				t.Errorf("err = %q, want the register's terminal period", message)
			}
		})
	}
}

// The supported list is generated from the same set the guard checks, so a
// kind can never be advertised as supported and then refused.
func TestUnsupportedKindMessageListsTheSetTheGuardUses(t *testing.T) {
	_, err := ScaleCommand{State: workload("cm", kinds.ConfigMap)}.Execute(1, 2)
	if err == nil {
		t.Fatal("scale accepted a ConfigMap")
	}
	for _, supported := range scalableKinds {
		if !strings.Contains(err.Error(), kinds.PluralDisplay(string(supported))) {
			t.Errorf("err = %q, want it to name %s, which scalableKinds accepts",
				err, supported)
		}
	}
}

// Debugging a node is a different operation wearing the same name: kubectl
// creates a privileged pod on the node rather than attaching a container to
// one. It is addressed node/NAME, and takes no -n — a Node is cluster-scoped,
// so state records no namespace for one, and kubectl places the debug pod in
// the context's current namespace rather than in one kx would have to invent.
func TestDebugOnANodeTargetsTheNode(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "node-a", kind: kinds.Node}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := joinArgs(kubectl.interactive[0])
	for _, want := range []string{"debug", "-it", "node/node-a", "--image=busybox"} {
		if !strings.Contains(got, want) {
			t.Errorf("args = %q, want them to contain %q", got, want)
		}
	}
	if strings.Contains(got, "-n ") {
		t.Errorf("args = %q, want no namespace flag — a Node is not in one", got)
	}
	// --target is what the pod path adds after a container lookup. A node has
	// no container to share a namespace with, so neither the flag nor the
	// lookup that finds it belongs here.
	if strings.Contains(got, "--target") {
		t.Errorf("args = %q, want no --target for a node", got)
	}
	if len(kubectl.runs) != 0 {
		t.Errorf("ran %v — a node needs no container lookup", kubectl.runs)
	}
}

// Refused rather than forwarded, so the message names the combination kx
// understands instead of leaving kubectl to reject a flag pairing kx chose.
func TestDebugOnANodeRefusesTarget(t *testing.T) {
	kubectl := &recordingKubectl{}
	resolver := fakeResolver{name: "node-a", kind: kinds.Node}

	err := DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}.Execute(1, nil, []string{"--target", "app"})

	if err == nil {
		t.Fatal("--target was accepted for a node")
	}
	if !strings.Contains(err.Error(), "--target") ||
		!strings.Contains(err.Error(), "Node") {
		t.Errorf("err = %q, want it to name --target and Node", err)
	}
	if len(kubectl.interactive) != 0 {
		t.Errorf("ran %v — the refusal comes before kubectl", kubectl.interactive)
	}
}

// The pod path is untouched: it still addresses the pod bare, scopes it to the
// namespace state recorded, and looks a container up to share a namespace with.
func TestDebugOnAPodIsUnchangedByTheNodePath(t *testing.T) {
	kubectl := &recordingKubectl{output: "app\n"}
	resolver := fakeResolver{name: "web", namespace: "prod", kind: kinds.Pod}

	if err := (DebugCommand{
		Kubectl: kubectl, State: resolver, Image: "busybox",
	}).Execute(1, nil, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := joinArgs(kubectl.interactive[0])
	for _, want := range []string{"debug -it web", "-n prod", "--target=app"} {
		if !strings.Contains(got, want) {
			t.Errorf("args = %q, want them to contain %q", got, want)
		}
	}
	if strings.Contains(got, "node/") {
		t.Errorf("args = %q, want a pod addressed bare", got)
	}
}
