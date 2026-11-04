package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
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

// A kind/name target that names nothing is reported as missing, in kx's
// words and the namespace it was looked for in, on every path a command
// fails by: kubectl's captured not-found (scale, yaml, delete), a streamed
// command's exit (describe, exec), an empty event list, and the API server's
// not-found (diag, tree). The listing is never run again — no listing named
// it. kx scale deploy/typo 3 relisted the user's saved pods, kx exec
// pod/typo said no shell was found, and kx events deploy/typo that it had no
// events.
func TestTypedTargetNotFoundIsReportedNotRefreshed(t *testing.T) {
	const notFound = `Error from server (NotFound): deployments.apps "gone" not found`
	const want = "Deployment/gone was not found in namespace 'prod'."
	for _, argv := range [][]string{
		{"scale", "deploy/gone", "3"},
		{"yaml", "deploy/gone"},
		{"delete", "deploy/gone", "--yes"},
		{"rollout", "restart", "deploy/gone"},
		{"labels", "deploy/gone"},
		{"describe", "deploy/gone"},
		{"exec", "deploy/gone", "--", "ls"},
		{"exec", "deploy/gone"},
		{"events", "deploy/gone"},
		{"diag", "deploy/gone"},
		{"tree", "deploy/gone"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			stdout, stderr := splitRender(t)
			kube := &recordingKubectl{err: kubectl.Error{Stderr: notFound}, exitCode: 1, probeCode: 1}
			services := withFakeKubernetes(staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}}))
			err := Execute(NewRoot(services, "test"), argv)
			if err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
			if out := stdout.String() + stderr.String(); strings.Contains(out, "refresh") {
				t.Errorf("a typed target's failure named the listing:\n%s", out)
			}
			for _, run := range kube.runs {
				if joinArgs(run) == "get pods" {
					t.Errorf("the saved listing was run again: %v", kube.runs)
				}
			}
		})
	}
	// Control: an index that fails the same way is refreshed, so the fixture
	// does reach the refresh the typed targets above are kept from.
	stdout, stderr := splitRender(t)
	kube := &recordingKubectl{err: kubectl.Error{Stderr: `Error from server (NotFound): pods "api-old" not found`}}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	_ = Execute(NewRoot(services, "test"), []string{"delete", "1", "--yes"})
	if out := stdout.String() + stderr.String(); !strings.Contains(out, "refresh") {
		t.Errorf("the index control was not refreshed — it no longer exercises the refresh:\n%s", out)
	}
}

// A not-found about something other than the target — a container, a
// resource the command read on the way — is reported as kubectl said it,
// and the target is not called missing.
func TestTypedTargetOtherNotFoundIsKubectls(t *testing.T) {
	const other = `error: container "sidecar" not found in pod "api-7d"`
	kube := &recordingKubectl{errs: []error{kubectl.Error{Stderr: other}}}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	splitRender(t)
	err := Execute(NewRoot(services, "test"), []string{"yaml", "deploy/api"})
	if err == nil || err.Error() != other {
		t.Errorf("err = %v, want kubectl's %q", err, other)
	}
}

// deletingKubectl is a cluster of Deployments and Services, keyed
// Kind/name: delete removes one, and asking for one that is not there
// answers as kubectl does, naming the resource it asked for.
type deletingKubectl struct {
	recordingKubectl
	gone map[string]bool
}

func (k *deletingKubectl) Run(args []string) (string, error) {
	k.runs = append(k.runs, args)
	key := args[1] + "/" + args[2]
	if k.gone[key] {
		resource := map[string]string{"Deployment": "deployments.apps", "Service": "services"}[args[1]]
		return "", kubectl.Error{Stderr: `Error from server (NotFound): ` + resource + ` "` + args[2] + `" not found`}
	}
	if args[0] == "delete" {
		k.gone[key] = true
	}
	return "", nil
}

// Of several targets, the one kubectl said it could not find is the one
// reported — not one the command itself deleted a moment before, which a
// check of every target in turn finds missing first. kubectl names the
// resource as well as the name, and both are read: deploy/api svc/api share
// a name, and the missing Service was reported as the Deployment kx had
// just deleted.
func TestTypedTargetNotFoundNamesTheOneKubectlCouldNotFind(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		gone string
		want string
	}{
		{[]string{"delete", "deploy/a", "deploy/b", "--yes"}, "Deployment/b", "Deployment/b was not found in namespace 'prod'."},
		{[]string{"delete", "deploy/api", "svc/api", "--yes"}, "Service/api", "Service/api was not found in namespace 'prod'."},
	} {
		kube := &deletingKubectl{gone: map[string]bool{tc.gone: true}}
		services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
		splitRender(t)
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: err = %v, want %q; kubectl got %v", tc.argv, err, tc.want, kube.runs)
		}
	}
}

// A kind kx does not name itself, a CRD, is matched to kubectl's reply by
// name: kubectl spells the resource its own way (widgets.example.com for a
// widget typed as widget.example.com), and kx cannot tell the spellings
// apart.
func TestTypedCRDNotFoundIsMatchedByName(t *testing.T) {
	splitRender(t)
	kube := &recordingKubectl{err: kubectl.Error{Stderr: `Error from server (NotFound): widgets.example.com "gone" not found`}}
	services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
	err := Execute(NewRoot(services, "test"), []string{"yaml", "widget.example.com/gone"})
	if want := "widget.example.com/gone was not found in namespace 'prod'."; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// A kind/name that names nothing is reported as missing on a line that also
// spends an index: the index came from a listing, the typo did not, and
// refreshing the listing answered kx yaml 1 deploy/typo with "Run 'kx get
// pods' to refresh the list." and kx labels 1 deploy/typo with the saved
// pods table. Control: when kubectl names the index's resource as well, the
// listing is what went stale, and it is refreshed.
func TestTypedTargetNotFoundBesideAnIndexIsReported(t *testing.T) {
	const want = "Deployment/gone was not found in namespace 'prod'."
	for _, argv := range [][]string{
		{"yaml", "1", "deploy/gone"},
		{"labels", "1", "deploy/gone"},
		{"delete", "1", "deploy/gone", "--yes"},
	} {
		for _, tc := range []struct {
			stderr  string
			refresh bool
		}{
			{`Error from server (NotFound): deployments.apps "gone" not found`, false},
			{"Error from server (NotFound): pods \"api-old\" not found\n" +
				`Error from server (NotFound): deployments.apps "gone" not found`, true},
		} {
			stdout, stderr := splitRender(t)
			kube := &recordingKubectl{err: kubectl.Error{Stderr: tc.stderr}, exitCode: 1, probeCode: 1}
			services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
			err := Execute(NewRoot(services, "test"), argv)
			refreshed := strings.Contains(stdout.String()+stderr.String(), "refresh")
			if refreshed != tc.refresh {
				t.Errorf("%v, kubectl %q: refreshed = %v, want %v:\n%s%s", argv, tc.stderr, refreshed, tc.refresh, stdout, stderr)
			}
			if !tc.refresh && (err == nil || err.Error() != want) {
				t.Errorf("%v: err = %v, want %q", argv, err, want)
			}
		}
	}
}

// The commands that change the cluster take kind/name too: each reaches
// kubectl naming the resource in the namespace it was settled in, and kx
// delete's prompt names that namespace.
func TestMutatingCommandsTakeTypedTargets(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		want   string
		prompt string
	}{
		{[]string{"delete", "deploy/api"}, "delete Deployment api -n prod", "Delete Deployment/api in prod?"},
		{[]string{"scale", "deploy/api", "3", "-n", "staging"}, "scale Deployment/api --replicas=3 -n staging", ""},
		{[]string{"rollout", "restart", "sts/db"}, "rollout restart StatefulSet/db -n prod", ""},
		{[]string{"cordon", "node/worker"}, "cordon worker", ""},
		{[]string{"exec", "deploy/api", "--", "ls"}, "exec -it Deployment/api -n prod -- ls", ""},
		{[]string{"taint", "node/worker", "k=v:NoSchedule"}, "taint node worker k=v:NoSchedule", ""},
	} {
		services, kube := typedServices(t)
		var prompts []string
		services.Confirm = func(prompt string) error { prompts = append(prompts, prompt); return nil }
		if err := Execute(NewRoot(services, "test"), tc.argv); err != nil {
			t.Fatalf("%v: %v", tc.argv, err)
		}
		calls := append(append([][]string{}, kube.runs...), kube.interactive...)
		if len(calls) == 0 || !strings.HasPrefix(joinArgs(calls[0]), tc.want) {
			t.Errorf("%v: kubectl got %v, want a call starting %q", tc.argv, calls, tc.want)
		}
		if tc.prompt != "" && (len(prompts) != 1 || prompts[0] != tc.prompt) {
			t.Errorf("%v: prompts = %q, want %q", tc.argv, prompts, tc.prompt)
		}
	}
}

// A prompt before something is removed names the cluster a flag chose, so
// "Delete Deployment/api in default?" is not asked of a delete bound for
// another context. With no flag the cluster is the current one, as it was,
// and the prompt is unchanged — the control that the phrase comes from the
// flag.
func TestPromptsNameTheClusterAFlagChose(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		prompt string
	}{
		{[]string{"delete", "deploy/api", "--context", "prod"}, "Delete Deployment/api in other-default on context 'prod'?"},
		{[]string{"delete", "deploy/api", "--cluster", "east"}, "Delete Deployment/api in other-default on cluster 'east'?"},
		{[]string{"delete", "deploy/api"}, "Delete Deployment/api in other-default?"},
		{[]string{"delete", "node/worker"}, "Delete Node/worker?"},
		{[]string{"delete", "node/worker", "--context", "prod"}, "Delete Node/worker on context 'prod'?"},
		{[]string{"delete", "deploy/api", "--context", "prod", "--server", "https://staging:6443"},
			"Delete Deployment/api in other-default on context 'prod', server 'https://staging:6443'?"},
		{[]string{"drain", "node/worker", "--context", "prod"}, "Evict all Pods from Node/worker on context 'prod'?"},
		{[]string{"drain", "node/worker"}, "Evict all Pods from Node/worker?"},
		{[]string{"taint", "node/worker", "k=v:NoExecute", "--context", "prod"},
			"Evict every Pod on Node/worker on context 'prod' that does not tolerate 'k=v:NoExecute'?"},
		{[]string{"taint", "node/worker", "k=v:NoExecute"},
			"Evict every Pod on Node/worker that does not tolerate 'k=v:NoExecute'?"},
	} {
		services, _ := typedServices(t)
		services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
		var prompts []string
		services.Confirm = func(prompt string) error {
			prompts = append(prompts, prompt)
			return errors.New("declined")
		}
		_ = Execute(NewRoot(services, "test"), tc.argv)
		if len(prompts) != 1 || prompts[0] != tc.prompt {
			t.Errorf("%v: prompts = %q, want %q", tc.argv, prompts, tc.prompt)
		}
	}
}

// A kind/name a flag sent to another cluster, found missing there, is
// reported with that cluster named, on every path that finds it missing:
// kubectl's captured reply (scale), a streamed command's exit (describe),
// and the API server's (diag). kx scale deploy/api 3 --context prod said
// only "in namespace 'default'", and the user looked for a typo on the
// cluster they were in. Control: with no flag nothing is added.
func TestTypedNotFoundNamesTheClusterAFlagChose(t *testing.T) {
	const notFound = `Error from server (NotFound): deployments.apps "gone" not found`
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"scale", "deploy/gone", "3", "--context", "prod"}, "Deployment/gone was not found in namespace 'other-default' on context 'prod'."},
		{[]string{"describe", "deploy/gone", "--context", "prod"}, "Deployment/gone was not found in namespace 'other-default' on context 'prod'."},
		{[]string{"diag", "deploy/gone", "--context", "prod"}, "Deployment/gone was not found in namespace 'other-default' on context 'prod'."},
		{[]string{"scale", "deploy/gone", "3"}, "Deployment/gone was not found in namespace 'other-default'."},
		{[]string{"diag", "deploy/gone"}, "Deployment/gone was not found in namespace 'other-default'."},
	} {
		splitRender(t)
		kube := &recordingKubectl{err: kubectl.Error{Stderr: notFound}, exitCode: 1, probeCode: 1}
		services := withFakeKubernetes(staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}}))
		services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
		services.KubernetesFor = func(k8s.Cluster) (kubernetes.Interface, error) { return services.Kubernetes() }
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v: err = %v, want %q", tc.argv, err, tc.want)
		}
	}
}

// A cluster flag beside kind/name reaches every kubectl call the command
// makes, not just the forwarded one: kx label reads the current labels first,
// and that read asked the current context's cluster while the write went to
// the one --context named.
func TestClusterFlagReachesACommandsOwnReads(t *testing.T) {
	services, kube := typedServices(t)
	services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
	kube.output = `{"metadata":{"name":"api","labels":{}}}`
	argv := []string{"label", "deploy/api", "env=prod", "--context", "other"}
	if err := Execute(NewRoot(services, "test"), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	if len(kube.runs) < 2 {
		t.Fatalf("label made %d kubectl calls, want its read and its write: %v", len(kube.runs), kube.runs)
	}
	for _, run := range kube.runs {
		if !strings.Contains(joinArgs(run), "--context=other") {
			t.Errorf("kubectl call %q is missing --context=other", joinArgs(run))
		}
	}
	// Control: without the flag, no call carries one.
	services, kube = typedServices(t)
	kube.output = `{"metadata":{"name":"api","labels":{}}}`
	if err := Execute(NewRoot(services, "test"), []string{"label", "deploy/api", "env=prod"}); err != nil {
		t.Fatalf("label without --context: %v", err)
	}
	for _, run := range kube.runs {
		if strings.Contains(joinArgs(run), "--context") {
			t.Errorf("kubectl call %q carries a --context nobody gave", joinArgs(run))
		}
	}
}

// A typed target came from no listing, so an agent's listing being current
// says nothing about it: no notice. The control spends an index out of the
// same agent listing, which does print one.
func TestTypedTargetIsNotAnAgentIndex(t *testing.T) {
	for _, tc := range []struct {
		argv   []string
		notice bool
	}{
		{[]string{"scale", "deploy/api", "3"}, false},
		{[]string{"scale", "1", "3"}, true},
	} {
		services, _ := typedServices(t)
		saveListing(t, services, kinds.Deployment, "prod", true, "api")
		_, stderr, err := runCaptured(t, NewRoot(services, "test"), tc.argv)
		if err != nil {
			t.Fatalf("%v: %v", tc.argv, err)
		}
		if got := strings.Contains(stderr, noticeSubstring); got != tc.notice {
			t.Errorf("%v: notice printed = %v, want %v (stderr %q)", tc.argv, got, tc.notice, stderr)
		}
	}
}

// kx mark pins a typed target only once the cluster confirms it exists, and
// pins it in the namespace it was settled in.
func TestMarkTypedTarget(t *testing.T) {
	services, kube := typedServices(t)
	if err := Execute(NewRoot(services, "test"), []string{"mark", "api", "deploy/api", "-n", "staging"}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if len(kube.runs) != 1 || joinArgs(kube.runs[0]) != "get Deployment api -n staging" {
		t.Errorf("runs = %v, want one 'get Deployment api -n staging'", kube.runs)
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
	kube.err = kubectl.Error{Stderr: `Error from server (NotFound): deployments.apps "nope" not found`}
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

// --context beside kind/name reaches the commands that read the API through
// client-go, which built their client from the current context whatever the
// flag said. Control: without the flag the shared client is used and no
// other is built.
func TestContextReachesClientGoReads(t *testing.T) {
	for _, argv := range [][]string{
		{"diag", "deploy/api", "--context", "other"},
		{"tree", "deploy/api", "--context", "other"},
		{"events", "deploy/api", "--context", "other"},
	} {
		services, _ := typedServices(t)
		var built []k8s.Cluster
		services.KubernetesFor = func(cluster k8s.Cluster) (kubernetes.Interface, error) {
			built = append(built, cluster)
			return fake.NewSimpleClientset(), nil
		}
		services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
		_ = Execute(NewRoot(services, "test"), argv)
		if len(built) == 0 || built[0].Context != "other" {
			t.Errorf("%v: clients built for %+v, want one for context other", argv, built)
		}

		services, _ = typedServices(t)
		services = withFakeKubernetes(services)
		built = nil
		services.KubernetesFor = func(cluster k8s.Cluster) (kubernetes.Interface, error) {
			built = append(built, cluster)
			return fake.NewSimpleClientset(), nil
		}
		_ = Execute(NewRoot(services, "test"), argv[:2])
		if len(built) != 0 {
			t.Errorf("%v without --context built a client for %+v", argv[:2], built)
		}
	}
}

// --context is refused on a sweep, whose rows would be numbered into this
// cluster's history, and beside an index, which carries its own cluster.
func TestContextRefusedWithoutATypedTarget(t *testing.T) {
	sweep := "'--context' needs a kind/name target — a sweep's rows are numbered, and an " +
		"index is spent in the cluster it was counted in. To sweep another cluster, " +
		"switch to it with 'kx context'."
	beside := "'--context' cannot be combined with an index — an index already carries the " +
		"cluster it was listed from. To act on another cluster, switch to it with 'kx context' " +
		"and list it there."
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"diag", "--context", "other"}, sweep},
		{[]string{"tree", "--context", "other"}, sweep},
		{[]string{"scan", "--context", "other"}, sweep},
		{[]string{"diag", "1", "--context", "other"}, beside},
		{[]string{"tree", "1", "--context", "other"}, beside},
		{[]string{"events", "1", "--context", "other"}, beside},
		{[]string{"labels", "1", "--context", "other"}, beside},
		{[]string{"mark", "api", "1", "--context", "other"}, beside},
	} {
		services, _ := typedServices(t)
		services = withFakeKubernetes(services)
		seedPodListing(t, services)
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%v:\n got %v\nwant %q", tc.argv, err, tc.want)
		}
	}
}

// A walk of another cluster is printed but never saved: its rows would be
// spent against this cluster. Control: the same walk without --context is
// numbered and saved.
func TestTreeOfAnotherClusterIsNotSaved(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"}}
	for _, tc := range []struct {
		argv  []string
		saved bool
	}{
		{[]string{"tree", "deploy/api", "-n", "prod", "--context", "other"}, false},
		{[]string{"tree", "deploy/api", "-n", "prod"}, true},
	} {
		services, _ := typedServices(t)
		client := fake.NewSimpleClientset(deployment)
		services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
		services.KubernetesFor = func(k8s.Cluster) (kubernetes.Interface, error) { return client, nil }
		if err := Execute(NewRoot(services, "test"), tc.argv); err != nil {
			t.Fatalf("%v: %v", tc.argv, err)
		}
		_, err := services.State.Load()
		if saved := err == nil; saved != tc.saved {
			t.Errorf("%v: listing saved = %v (Load err %v), want %v", tc.argv, saved, err, tc.saved)
		}
	}
}

// kx mark reports a failure to ask the cluster as itself. It checked with an
// exit code alone, so a kubectl missing from PATH, an expired login or a bad
// context all read as "Deployment/api was not found" and sent the user
// looking for a typo. The control is the not-found reply, in
// TestMarkTypedTarget.
func TestMarkTypedTargetReportsWhyTheCheckFailed(t *testing.T) {
	services, kube := typedServices(t)
	missing := "kubectl not found on PATH — install kubectl (https://kubernetes.io/docs/tasks/tools/) and ensure it is on your PATH."
	kube.err = errors.New(missing)
	err := Execute(NewRoot(services, "test"), []string{"mark", "api", "deploy/api"})
	if err == nil || err.Error() != missing {
		t.Errorf("err = %v, want %q", err, missing)
	}
	marks, _ := services.State.Marks()
	if _, ok := marks["api"]; ok {
		t.Error("a mark was saved though the cluster was never asked")
	}
}

// kx mark --context checks the resource in that cluster and records the
// mark against it, so spending the mark from another context is refused as
// any cross-context mark is.
func TestMarkTypedTargetInAnotherContext(t *testing.T) {
	services, kube := typedServices(t)
	services.ClusterNamespace = func(k8s.Cluster) string { return "other-default" }
	argv := []string{"mark", "api", "deploy/api", "--context", "other"}
	if err := Execute(NewRoot(services, "test"), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	if len(kube.runs) != 1 || !strings.Contains(joinArgs(kube.runs[0]), "--context=other") {
		t.Errorf("runs = %v, want one carrying --context=other", kube.runs)
	}
	marks, _ := services.State.Marks()
	if got := marks["api"]; got.Context != "other" || got.Namespace != "other-default" {
		t.Errorf("mark = %+v, want context other, namespace other-default", got)
	}
}

// A --context naming the current context is no other cluster: k9s passes
// its context on every plugin call, and a tree from k9s would otherwise
// never be numbered. The current context is "test" (recordingKubectl).
func TestTreeWithTheCurrentContextIsSaved(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"}}
	services, _ := typedServices(t)
	client := fake.NewSimpleClientset(deployment)
	services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	services.KubernetesFor = func(k8s.Cluster) (kubernetes.Interface, error) { return client, nil }
	argv := []string{"tree", "deploy/api", "-n", "prod", "--context", "test"}
	if err := Execute(NewRoot(services, "test"), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	if _, err := services.State.Load(); err != nil {
		t.Errorf("tree with the current context saved nothing: %v", err)
	}
}

// A streamed command that fails, whose kind/name check then fails to ask
// too, keeps kubectl's exit code and adds nothing: kubectl has already said
// why. kx describe deploy/api on an unreachable cluster printed the
// connection error a second time, inside kx's own failure line, and kx exec
// deploy/api -- test -f /x lost the command's exit code to it.
func TestTypedCheckThatCannotAskKeepsTheExitCode(t *testing.T) {
	const unreachable = "The connection to the server 127.0.0.1:1 was refused - did you specify the right host or port?"
	for _, tc := range []struct {
		argv []string
		code int
	}{
		{[]string{"describe", "deploy/api"}, 1},
		{[]string{"exec", "deploy/api", "--", "test", "-f", "/x"}, 2},
	} {
		splitRender(t)
		kube := &recordingKubectl{err: kubectl.Error{Stderr: unreachable}, exitCode: tc.code}
		services := staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{}})
		err := Execute(NewRoot(services, "test"), tc.argv)
		if err == nil || strings.Contains(err.Error(), unreachable) {
			t.Errorf("%v: err = %v, want kubectl's exit with nothing added", tc.argv, err)
		}
		var silent SilentError
		var exit ExitError
		code := -1
		switch {
		case errors.As(err, &silent):
			code = silent.Code
		case errors.As(err, &exit):
			code = exit.Code
		}
		if code != tc.code {
			t.Errorf("%v: exit code = %d, want kubectl's %d", tc.argv, code, tc.code)
		}
		if len(kube.runs) == 0 {
			t.Errorf("%v: the check never ran, so the test exercises nothing", tc.argv)
		}
	}
}
