package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// runWait runs kx wait over a listing of kind in namespace.
func runWait(
	t *testing.T, kube *recordingKubectl, kind kinds.Kind, namespace string, names []string,
	objects []runtime.Object, args ...string,
) (stdout string, err error) {
	t.Helper()
	services := switchServices(t, kube)
	services.Kubernetes = func() (kubernetes.Interface, error) {
		return fake.NewSimpleClientset(objects...), nil
	}
	saveListing(t, services, kind, namespace, false, names...)
	stdout, _, err = runCaptured(t, newWaitCommand(services), args)
	return stdout, err
}

// Each kind with one unambiguous meaning of "ready" is waited for without a
// --for, and the line names what was met.
func TestWaitDefaultsByKind(t *testing.T) {
	for _, tc := range []struct {
		kind      kinds.Kind
		namespace string
		wantArgv  string
		wantLine  string
	}{
		{kinds.Pod, "prod", "wait Pod/api -n prod --for=condition=Ready", "✓ Pod/api · prod · Ready"},
		{kinds.Node, "", "wait Node/api --for=condition=Ready", "✓ Node/api · Ready"},
		{kinds.PersistentVolumeClaim, "prod",
			"wait PersistentVolumeClaim/api -n prod --for=jsonpath={.status.phase}=Bound",
			"✓ PersistentVolumeClaim/api · prod · Bound"},
	} {
		kube := &recordingKubectl{}
		stdout, err := runWait(t, kube, tc.kind, tc.namespace, []string{"api"}, nil, "1")
		if err != nil {
			t.Fatalf("kx wait on a %s: %v", tc.kind, err)
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != tc.wantArgv {
			t.Errorf("%s: kubectl = %q, want %q", tc.kind, kube.runs, tc.wantArgv)
		}
		if !strings.Contains(stdout, tc.wantLine) {
			t.Errorf("%s: stdout = %q, want %q", tc.kind, stdout, tc.wantLine)
		}
	}
}

// A --for is the caller's, for any kind — a rollout kind included — and
// replaces the default rather than joining it.
func TestWaitForOverridesTheDefault(t *testing.T) {
	for _, tc := range []struct {
		kind     kinds.Kind
		args     []string
		wantArgv string
		wantLine string
	}{
		{kinds.Deployment, []string{"1", "--for=condition=Available"},
			"wait Deployment/api -n prod --for=condition=Available", "· condition=Available"},
		{kinds.Pod, []string{"1", "--for=delete", "--timeout=2m"},
			"wait Pod/api -n prod --for=delete --timeout=2m", "· deleted"},
		// kubectl requires every --for, so the line names each one, in either
		// spelling. It named only the last: the flag was read with the
		// helper that keeps the final occurrence.
		{kinds.Pod, []string{"1", "--for=condition=Ready", "--for", "condition=Initialized"},
			"wait Pod/api -n prod --for=condition=Ready --for condition=Initialized",
			"· condition=Ready, condition=Initialized"},
	} {
		kube := &recordingKubectl{}
		stdout, err := runWait(t, kube, tc.kind, "prod", []string{"api"}, nil, tc.args...)
		if err != nil {
			t.Fatalf("kx wait %v: %v", tc.args, err)
		}
		if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != tc.wantArgv {
			t.Errorf("kubectl = %q, want %q", kube.runs, tc.wantArgv)
		}
		if !strings.Contains(stdout, tc.wantLine) {
			t.Errorf("stdout = %q, want %q", stdout, tc.wantLine)
		}
	}
}

// Without a --for, a kind with no single meaning of ready is refused before
// kubectl runs: a rollout kind points at the command that knows, anything
// else asks for a condition.
func TestWaitRefusesKindsWithoutADefault(t *testing.T) {
	for _, tc := range []struct {
		kind kinds.Kind
		want string
	}{
		{kinds.Deployment, "waited for with 'kx rollout status 1'"},
		{kinds.StatefulSet, "waited for with 'kx rollout status 1'"},
		{kinds.ConfigMap, "no default condition for a ConfigMap — pass one with --for"},
	} {
		kube := &recordingKubectl{}
		_, err := runWait(t, kube, tc.kind, "prod", []string{"api"}, nil, "1")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.kind, err, tc.want)
		}
		if len(kube.runs) != 0 {
			t.Errorf("%s: kubectl ran %q", tc.kind, kube.runs)
		}
	}
}

// Every index is resolved before anything waits, so a bad one late in the
// list costs no waiting; then they are waited for in order, and the first
// failure ends the command.
func TestWaitResolvesAllThenStopsAtTheFirstFailure(t *testing.T) {
	kube := &recordingKubectl{}
	if _, err := runWait(t, kube, kinds.Pod, "prod", []string{"a", "b"}, nil, "1", "99"); err == nil {
		t.Error("kx wait 1 99 on a two-row listing succeeded")
	}
	if len(kube.runs) != 0 {
		t.Errorf("waited on %q before the bad index was refused", kube.runs)
	}

	kube = &recordingKubectl{errs: []error{kubectl.Error{Stderr: "timed out waiting for the condition"}}}
	if _, err := runWait(t, kube, kinds.Pod, "prod", []string{"a", "b"}, nil, "1..2"); err == nil {
		t.Error("kx wait 1..2 succeeded with the first wait failing")
	}
	if len(kube.runs) != 1 {
		t.Errorf("kubectl ran %d times, want 1 — the wait after a failure must not start", len(kube.runs))
	}
}

func TestWaitRefusesAnInvalidTimeout(t *testing.T) {
	kube := &recordingKubectl{}
	_, err := runWait(t, kube, kinds.Pod, "prod", []string{"api"}, nil, "1", "--timeout=soon")
	if err == nil || !strings.Contains(err.Error(), "'soon' is not a duration") {
		t.Errorf("err = %v, want the timeout refusal", err)
	}
}

// Only a LoadBalancer is ever given an address; any other Service type would
// only time out, so it is refused naming the type.
func TestWaitOnAService(t *testing.T) {
	service := func(kind corev1.ServiceType) runtime.Object {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
			Spec:       corev1.ServiceSpec{Type: kind},
		}
	}
	kube := &recordingKubectl{}
	stdout, err := runWait(t, kube, kinds.Service, "prod", []string{"api"},
		[]runtime.Object{service(corev1.ServiceTypeLoadBalancer)}, "1")
	if err != nil {
		t.Fatalf("kx wait on a LoadBalancer: %v", err)
	}
	if want := "wait Service/api -n prod --for=jsonpath={.status.loadBalancer.ingress}"; len(kube.runs) != 1 ||
		joinArgs(kube.runs[0]) != want {
		t.Errorf("kubectl = %q, want %q", kube.runs, want)
	}
	if !strings.Contains(stdout, "Service/api · prod · has an address") {
		t.Errorf("stdout = %q", stdout)
	}

	kube = &recordingKubectl{}
	_, err = runWait(t, kube, kinds.Service, "prod", []string{"api"},
		[]runtime.Object{service(corev1.ServiceTypeClusterIP)}, "1")
	if err == nil || !strings.Contains(err.Error(), "is a ClusterIP Service") {
		t.Errorf("err = %v, want the ClusterIP refusal", err)
	}
	if len(kube.runs) != 0 {
		t.Errorf("kubectl ran %q for a ClusterIP Service", kube.runs)
	}
}

func job(conditions ...batchv1.JobCondition) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "prod"},
		Status:     batchv1.JobStatus{Conditions: conditions},
	}
}

func jobCondition(kind batchv1.JobConditionType, reason, message string) batchv1.JobCondition {
	return batchv1.JobCondition{Type: kind, Status: corev1.ConditionTrue, Reason: reason, Message: message}
}

var jobTarget = Resolved{Ref: state.Ref{Index: 1}, Kind: kinds.Job, Name: "migrate", Namespace: "prod"}

// waitForJob runs the Job wait against client, with watcher standing in for
// the API's watch when given.
func waitForJob(t *testing.T, client *fake.Clientset, watcher watch.Interface, timeout time.Duration) (string, error) {
	t.Helper()
	if watcher != nil {
		client.PrependWatchReactor("jobs", func(k8stesting.Action) (bool, watch.Interface, error) {
			return true, watcher, nil
		})
	}
	command := WaitCommand{
		Kubernetes: func() (kubernetes.Interface, error) { return client, nil }, Status: noStatus,
	}
	return command.Execute(context.Background(), jobTarget, timeout, nil)
}

// A Job that already finished returns at once, either way it finished; a
// failed one says why, rather than waiting out the timeout for a Complete
// that will never come.
func TestWaitForAJobThatAlreadyFinished(t *testing.T) {
	met, err := waitForJob(t, fake.NewSimpleClientset(job(jobCondition(batchv1.JobComplete, "", ""))), nil, time.Second)
	if err != nil || met != "Complete" {
		t.Errorf("complete Job: met=%q err=%v, want Complete", met, err)
	}
	_, err = waitForJob(t, fake.NewSimpleClientset(job(
		jobCondition(batchv1.JobFailed, "BackoffLimitExceeded", "Job has reached the specified backoff limit"))),
		nil, time.Second)
	if want := "Job/migrate failed: BackoffLimitExceeded — Job has reached the specified backoff limit"; err == nil ||
		err.Error() != want {
		t.Errorf("failed Job: err = %v, want %q", err, want)
	}
}

// A Job still running is watched until it finishes — here, fails — and the
// failure ends the wait as soon as it is seen.
func TestWaitWatchesARunningJobUntilItFinishes(t *testing.T) {
	watcher := watch.NewFake()
	go watcher.Modify(job(jobCondition(batchv1.JobFailed, "DeadlineExceeded", "")))
	_, err := waitForJob(t, fake.NewSimpleClientset(job()), watcher, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "failed: DeadlineExceeded") {
		t.Errorf("err = %v, want the failure the watch delivered", err)
	}
}

// A Job that never finishes runs out the timeout, and says so.
func TestWaitForAJobTimesOut(t *testing.T) {
	watcher := watch.NewFake()
	_, err := waitForJob(t, fake.NewSimpleClientset(job()), watcher, 50*time.Millisecond)
	if err == nil || err.Error() != "Timed out after 50ms waiting for Job/migrate." {
		t.Errorf("err = %v, want the timeout message", err)
	}
}

// A Job that is gone is the stale-index error withRefresh relists on.
func TestWaitForAVanishedJobIsStale(t *testing.T) {
	_, err := waitForJob(t, fake.NewSimpleClientset(), nil, time.Second)
	var stale StaleResourceError
	if !errors.As(err, &stale) || !isStale(err) {
		t.Errorf("err = %v, want a refreshable StaleResourceError", err)
	}
}

// jobServer serves one Job to a real client-go client, and counts the
// requests that are anything but a read of it.
//
// A real client rather than the fake, because the fake ignores its context:
// the bug this guards was a context already past its deadline, which a real
// request refuses before it is sent, and the fake answers regardless.
func jobServer(t *testing.T, served *batchv1.Job) (kubernetes.Interface, *atomic.Int32) {
	t.Helper()
	var others atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/apis/batch/v1/namespaces/prod/jobs/migrate" {
			others.Add(1)
			http.NotFound(w, r)
			return
		}
		served.APIVersion, served.Kind = "batch/v1", "Job"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(served)
	}))
	t.Cleanup(server.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("client for %s: %v", server.URL, err)
	}
	return client, &others
}

// kubectl reads --timeout=0 as "check once" and a negative one as a week. kx's
// own Job wait handed either straight to context.WithTimeout, whose deadline
// had then already passed, so the Job's first read failed before it was sent:
// `kx wait 1 --timeout=0` on a Job that had finished said "Timed out after 0s".
func TestWaitForAFinishedJobWithAZeroOrNegativeTimeout(t *testing.T) {
	for _, value := range []string{"0", "0s", "-1s"} {
		client, _ := jobServer(t, job(jobCondition(batchv1.JobComplete, "", "")))
		timeout, err := waitTimeout([]string{"--timeout=" + value})
		if err != nil {
			t.Fatalf("waitTimeout(%s): %v", value, err)
		}
		met, err := WaitCommand{
			Kubernetes: func() (kubernetes.Interface, error) { return client, nil }, Status: noStatus,
		}.Execute(context.Background(), jobTarget, timeout, nil)
		if err != nil || met != "Complete" {
			t.Errorf("--timeout=%s on a complete Job: met=%q err=%v, want Complete", value, met, err)
		}
	}
}

// Checking once means exactly that: a Job still running is reported as not
// finished at once, without a watch being opened for it.
func TestWaitForARunningJobWithAZeroTimeoutChecksOnce(t *testing.T) {
	client, others := jobServer(t, job())
	_, err := WaitCommand{
		Kubernetes: func() (kubernetes.Interface, error) { return client, nil }, Status: noStatus,
	}.Execute(context.Background(), jobTarget, 0, nil)
	if err == nil || err.Error() != "Timed out after 0s waiting for Job/migrate." {
		t.Errorf("err = %v, want the timeout message", err)
	}
	if n := others.Load(); n != 0 {
		t.Errorf("made %d requests besides the one read, want none", n)
	}
}
