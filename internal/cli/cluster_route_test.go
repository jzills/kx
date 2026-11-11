package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// clusterRouteArgv is how TestEveryKindNameCommandSendsEveryCallToTheContext
// drives each command that takes kind/name, keyed by its path below kx.
// Every such command must have an entry: one added without one fails the
// test rather than going unchecked.
var clusterRouteArgv = map[string][]string{
	"mark":         {"mark", "api", "deploy/api"},
	"describe":     {"describe", "deploy/api"},
	"logs":         {"logs", "deploy/api"},
	"edit":         {"edit", "deploy/api"},
	"exec":         {"exec", "pod/api"},
	"debug":        {"debug", "pod/api"},
	"cordon":       {"cordon", "node/worker"},
	"uncordon":     {"uncordon", "node/worker"},
	"delete":       {"delete", "deploy/api", "--yes"},
	"drain":        {"drain", "node/worker", "--yes"},
	"taint":        {"taint", "node/worker", "k=v:NoSchedule"},
	"scale":        {"scale", "deploy/api", "3"},
	"patch":        {"patch", "deploy/api", "-p", "{}"},
	"wait":         {"wait", "deploy/api", "--for=condition=Available", "--timeout=0"},
	"set image":    {"set", "image", "deploy/api", "api=nginx:2"},
	"rollout":      {"rollout", "restart", "deploy/api"},
	"port-forward": {"port-forward", "svc/api", "8080:80"},
	"yaml":         {"yaml", "deploy/api"},
	"tree":         {"tree", "deploy/api"},
	"scan":         {"scan", "deploy/api", "--engine", "trivy"},
	"events":       {"events", "deploy/api"},
	"diagnostic":   {"diag", "deploy/api"},
	"labels":       {"labels", "deploy/api"},
	"annotations":  {"annotations", "deploy/api"},
	"label":        {"label", "deploy/api", "env=prod"},
	"annotate":     {"annotate", "deploy/api", "team=core"},
}

// Beside kind/name, --context sends the whole command to that context: every
// kubectl call it makes, its own reads and checks included, carries the flag
// once, and any API client is built for that context, once. Each command
// gets there by a forCluster call of its own, and deleting that call from
// any of seventeen commands left the suite green — the forwarded call still
// carried the flag, so only the command's own reads, and after a failure
// its check that the target exists, went to the current context instead.
//
// Driven twice, succeeding and failing, so the check a failure makes is
// among the calls looked at.
func TestEveryKindNameCommandSendsEveryCallToTheContext(t *testing.T) {
	root := NewRoot(argvServices(t), "test")
	var typed []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Annotations["arg.index"] == typedArgDoc {
			typed = append(typed, strings.TrimPrefix(cmd.CommandPath(), "kx "))
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(typed) < 20 {
		t.Fatalf("found %d commands taking kind/name — the walk no longer finds them: %v", len(typed), typed)
	}
	for _, path := range typed {
		argv, ok := clusterRouteArgv[path]
		if !ok {
			t.Errorf("kx %s takes kind/name but has no entry in clusterRouteArgv", path)
			continue
		}
		for _, failing := range []bool{false, true} {
			kube := &recordingKubectl{output: `{"metadata":{"name":"api"}}`}
			if failing {
				// Naming the target, as kubectl does, so the check kx makes
				// of the one kubectl could not find is among the calls.
				kube.err = kubectl.Error{Stderr: `Error from server (NotFound): "` + targetName(argv) + `" not found`}
				kube.exitCode, kube.probeCode = 1, 1
			}
			services := argvServices(t)
			services.Kubectl = kube
			services.Scanner = &fakeScanner{captures: []captured{{image: "api:v1", stdout: `{"Results":[]}`}}}
			services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
			var built []string
			services.KubernetesFor = func(cluster k8s.Cluster) (kubernetes.Interface, error) {
				built = append(built, cluster.Context)
				return fake.NewSimpleClientset(), nil
			}
			services.Kubernetes = func() (kubernetes.Interface, error) {
				built = append(built, "the current context")
				return fake.NewSimpleClientset(), nil
			}
			services.Confirm = func(string) error { return nil }
			quietRender(t)
			err := Execute(NewRoot(services, "test"), append(argv[:len(argv):len(argv)], "--context", "other"))
			if testing.Verbose() {
				t.Logf("kx %s (failing=%v): %v", path, failing, err)
			}

			calls := append(append(append([][]string{}, kube.runs...), kube.interactive...), kube.probes...)
			if len(calls) == 0 && len(built) == 0 {
				t.Errorf("kx %s (failing=%v) made no kubectl call and built no client — the argv no longer reaches it",
					path, failing)
			}
			for _, call := range calls {
				n := 0
				for _, arg := range call {
					if arg == "--context=other" {
						n++
					}
				}
				if n != 1 {
					t.Errorf("kx %s (failing=%v): kubectl %q carries --context=other %d times, want once",
						path, failing, joinArgs(call), n)
				}
			}
			if len(built) > 1 || (len(built) == 1 && built[0] != "other") {
				t.Errorf("kx %s (failing=%v): API clients built for %q, want at most one, for other",
					path, failing, built)
			}
		}
	}
}

// A command asks for its API client as often as it needs it, and beside
// --context the client is built once: kx wait asked once per Job, loading
// the kubeconfig and building a client for each.
func TestAContextsClientIsBuiltOnce(t *testing.T) {
	done := func(name string) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "other-default"},
			Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			}},
		}
	}
	services, _ := typedServices(t)
	services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
	builds := 0
	services.KubernetesFor = func(k8s.Cluster) (kubernetes.Interface, error) {
		builds++
		return fake.NewSimpleClientset(done("a"), done("b"), done("c")), nil
	}
	argv := []string{"wait", "job/a", "job/b", "job/c", "--timeout=0", "--context", "other"}
	if err := Execute(NewRoot(services, "test"), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	if builds != 1 {
		t.Errorf("%v built %d clients, want 1", argv, builds)
	}
}

// targetName is the name in the first kind/name of argv.
func targetName(argv []string) string {
	for _, arg := range argv {
		if _, name, ok := strings.Cut(arg, "/"); ok && typedShaped(arg) {
			return name
		}
	}
	return ""
}
