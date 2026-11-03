package cli

import (
	"strings"
	"testing"

	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// typedServices is argvServices with a recording kubectl and no listing
// saved: a typed target must resolve with no state at all.
func typedServices(t *testing.T) (Services, *recordingKubectl) {
	t.Helper()
	quietRender(t)
	kube := &recordingKubectl{}
	services := argvServices(t)
	services.Kubectl = kube
	return services, kube
}

func seedPodListing(t *testing.T, services Services) {
	t.Helper()
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"nginx"}, kinds.Pod),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// kind/name reaches kubectl as the resource it names, in the current
// namespace when no -n is given and in -n's when one is, with no listing
// saved — the whole point of a typed target is that it needs none.
func TestTypedTargetReachesKubectlWithoutAListing(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"describe", "deploy/api"}, "describe Deployment/api -n prod"},
		{[]string{"describe", "deploy/api", "-n", "staging"}, "describe Deployment/api -n staging"},
		{[]string{"describe", "Deployment/api", "svc/web"}, "describe Service/web -n prod"},
	} {
		services, kube := typedServices(t)
		if err := Execute(NewRoot(services, "test"), tc.argv); err != nil {
			t.Fatalf("%v: %v", tc.argv, err)
		}
		if len(kube.interactive) == 0 {
			t.Fatalf("%v: kubectl was never run", tc.argv)
		}
		last := joinArgs(kube.interactive[len(kube.interactive)-1])
		if !strings.HasPrefix(last, tc.want) {
			t.Errorf("%v: kubectl got %q, want it to start %q", tc.argv, last, tc.want)
		}
	}
}

// Without --context, the namespace is the kubeconfig's current one. With it,
// it is the default namespace of the context --context names, as kubectl's
// would be — not the current context's, which is a different cluster's.
func TestTypedTargetTakesTheNamespaceOfTheContextNamed(t *testing.T) {
	services, kube := typedServices(t)
	var asked k8s.Cluster
	services.ClusterNamespace = func(cluster k8s.Cluster) string {
		asked = cluster
		if cluster.Context == "staging" {
			return "staging-default"
		}
		return "elsewhere"
	}
	argv := []string{"describe", "deploy/api", "--context", "staging"}
	if err := Execute(NewRoot(services, "test"), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	if asked.Context != "staging" {
		t.Fatalf("namespace was asked for cluster %+v, want context staging", asked)
	}
	got := joinArgs(kube.interactive[0])
	if !strings.Contains(got, "-n staging-default") || !strings.Contains(got, "--context=staging") {
		t.Errorf("kubectl got %q, want -n staging-default and --context=staging", got)
	}
}

// Beside an index, -n and --context keep their refusal: the index carries
// both from its listing, and a typed target on the same line doesn't change
// that.
func TestScopeFlagsRefusedWhenAnIndexIsOnTheLine(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"describe", "1", "deploy/api", "-n", "staging"},
			"'--namespace' cannot be combined with an index — an index already carries the namespace it was listed from."},
		{[]string{"describe", "1", "deploy/api", "--context", "staging"},
			"'--context' cannot be combined with an index — an index already carries the cluster it was listed from. " +
				"To act on another cluster, switch to it with 'kx context' and list it there."},
	} {
		services, kube := typedServices(t)
		seedPodListing(t, services)
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: err = %v, want %q", tc.argv, err, tc.want)
		}
		if len(kube.interactive) != 0 {
			t.Errorf("%v: kubectl ran %v after the refusal", tc.argv, kube.interactive)
		}
	}
	// Control: the same line without the flag runs both targets.
	services, kube := typedServices(t)
	seedPodListing(t, services)
	if err := Execute(NewRoot(services, "test"), []string{"describe", "1", "deploy/api"}); err != nil {
		t.Fatalf("mixed line without a flag: %v", err)
	}
	if len(kube.interactive) != 2 {
		t.Errorf("mixed line ran %d kubectl calls, want 2: %v", len(kube.interactive), kube.interactive)
	}
}

// The refusals a typed target gets, each by its whole sentence.
func TestTypedTargetRefusals(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"describe", "deploy/api", "-A"},
			"'--all-namespaces' cannot be combined with kind/name — it names one resource, in one namespace."},
		{[]string{"describe", "node/worker", "-n", "prod"},
			"'--namespace' cannot be combined with Nodes — they live outside any namespace, so there is no scope to set. Drop the flag."},
		{[]string{"describe", "deploy/"},
			"'deploy/' is not a resource — name one as kind/name, such as deploy/api."},
		{[]string{"describe", "deploy/api/x"},
			"'deploy/api/x' is not a resource — name one as kind/name, such as deploy/api."},
		{[]string{"describe", "all/api"},
			"'all' is not one resource type — give the resource's own kind."},
		{[]string{"describe", "deploy/API"},
			"Name 'API' is not a Kubernetes name — use lowercase letters, digits, '-', '.' and ':', starting with a letter or digit."},
		{[]string{"describe", "deploy/api", "-n", "Bad_NS"},
			"Namespace 'Bad_NS' is not a Kubernetes namespace — use lowercase letters, digits and '-', starting and ending with a letter or digit."},
		{[]string{"diag", "deploy/api", "-A"},
			"'--all-namespaces' cannot be combined with kind/name — it names one resource, in one namespace. " +
				"Drop the flag, or drop the kind/name to sweep every namespace instead."},
		{[]string{"tree", "deploy/api", "-m", "web"},
			"'--match' cannot be combined with kind/name — it already names one resource. " +
				"Drop the flag, or drop the kind/name to sweep the namespace instead."},
	} {
		services, kube := typedServices(t)
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v:\n got %v\nwant %q", tc.argv, err, tc.want)
		}
		if len(kube.interactive) != 0 {
			t.Errorf("%v: kubectl ran %v after the refusal", tc.argv, kube.interactive)
		}
	}
}

// A typed target that kubectl can't find is reported as kubectl reported it:
// no existence probe (which would ask without the command's cluster flags),
// and no relist of the saved listing, which the target never came from.
func TestTypedTargetFailureIsNotRefreshed(t *testing.T) {
	services, kube := typedServices(t)
	seedPodListing(t, services)
	kube.exitCode, kube.probeCode = 1, 1
	err := Execute(NewRoot(services, "test"), []string{"describe", "deploy/gone"})
	if err == nil {
		t.Fatal("describe of a missing resource succeeded")
	}
	if len(kube.probes) != 0 {
		t.Errorf("probed %v for a typed target", kube.probes)
	}
	for _, run := range kube.runs {
		if len(run) > 0 && run[0] == "get" {
			t.Errorf("relisted with %v after a typed target failed", run)
		}
	}
	// Control: an index that fails the same way is probed.
	services, kube = typedServices(t)
	seedPodListing(t, services)
	kube.exitCode, kube.probeCode = 1, 1
	_ = Execute(NewRoot(services, "test"), []string{"describe", "1"})
	if len(kube.probes) == 0 {
		t.Error("an index that failed was not probed — the control no longer exercises the probe")
	}
}

// Commands that change the cluster don't take kind/name yet: their confirm
// prompts and agent notice are reviewed in their own change.
func TestMutatingCommandsStillRefuseTypedTargets(t *testing.T) {
	services, kube := typedServices(t)
	err := Execute(NewRoot(services, "test"), []string{"delete", "deploy/api"})
	want := "Invalid value for 'indexes': 'deploy/api' is not a valid int."
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if len(kube.runs)+len(kube.interactive) != 0 {
		t.Errorf("kubectl ran: %v %v", kube.runs, kube.interactive)
	}
}

// kx mark pins a typed target only once the cluster confirms it exists, and
// pins it in the namespace it was settled in.
func TestMarkTypedTarget(t *testing.T) {
	services, kube := typedServices(t)
	if err := Execute(NewRoot(services, "test"), []string{"mark", "api", "deploy/api", "-n", "staging"}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if len(kube.probes) != 1 || joinArgs(kube.probes[0]) != "get Deployment api -n staging" {
		t.Errorf("probes = %v, want one 'get Deployment api -n staging'", kube.probes)
	}
	marks, err := services.State.Marks()
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	got, ok := marks["api"]
	if !ok {
		t.Fatal("no mark 'api' was saved")
	}
	if got.Name != "api" || got.Kind != kinds.Deployment || got.Namespace != "staging" {
		t.Errorf("mark = %+v, want Deployment/api in staging", got)
	}

	services, kube = typedServices(t)
	kube.probeCode = 1
	err = Execute(NewRoot(services, "test"), []string{"mark", "api", "deploy/nope"})
	want := "Deployment/nope was not found in namespace 'prod'."
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	marks, _ = services.State.Marks()
	if _, ok := marks["api"]; ok {
		t.Error("a mark was saved for a resource the cluster doesn't have")
	}
}
