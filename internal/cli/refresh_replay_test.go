package cli

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// staleClusterServices is staleServices with a cluster for client-go to read,
// which a sweep or a tree is re-run against.
func staleClusterServices(
	t *testing.T, kube kubectl.Service, query *state.Query, objects ...runtime.Object,
) Services {
	t.Helper()
	services := staleServices(t, kube, query)
	client := fake.NewSimpleClientset(objects...)
	services.Kubernetes = func() (kubernetes.Interface, error) { return client, nil }
	return services
}

// runStale runs a command that fails as a vanished index does, returning
// what reached the screen.
func runStale(t *testing.T, services Services) string {
	t.Helper()
	out := captureRender(t)
	cmd := staleCommand(services)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("stale command returned no error")
	}
	return out.String()
}

func currentQuery(t *testing.T, services Services) *state.Query {
	t.Helper()
	return currentEntry(t, services).Query
}

func currentEntry(t *testing.T, services Services) state.State {
	t.Helper()
	entry, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return entry
}

// A stale index from kx top was refreshed into a kx get pods table: the
// query was recorded as get pods so the one replay there was could run it.
// The listing a user goes back to should be the one they were reading, so
// kx top is run again.
func TestStaleRefreshRunsKxTopAgain(t *testing.T) {
	kube := &fakeKubectl{
		output:    "NAME      CPU(cores)   MEMORY(bytes)\napi-new   1m           2Mi\n",
		namespace: "prod",
	}
	services := staleServices(t, kube,
		&state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}})

	out := runStale(t, services)
	if got := joinArgs(kube.args); got != "top pods -n prod" {
		t.Errorf("kubectl %q, want kx top's own call", got)
	}
	if !strings.Contains(out, "CPU(cores)") || !strings.Contains(out, "api-new") {
		t.Errorf("output = %q, want the usage table again", out)
	}
	if query := currentQuery(t, services); query == nil || query.Command != state.CommandTop {
		t.Errorf("current query = %+v, want kx top's", query)
	}
}

// A sweep had no query, so a stale index from one was answered "Run 'kx get
// <resource>'" — not the command that listed it. It is swept again.
func TestStaleRefreshSweepsAgain(t *testing.T) {
	services := staleClusterServices(t, &fakeKubectl{namespace: "prod"},
		&state.Query{Command: state.CommandDiag, Args: []string{"-n", "prod"}},
		brokenDeployment("api", "prod"), healthyDeployment("web", "prod"))

	out := runStale(t, services)
	for _, want := range []string{"State was stale", "Mixed · prod · 2 checked", "api"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q\n  missing %q", out, want)
		}
	}
	if strings.Contains(out, "to refresh the list") {
		t.Errorf("output = %q, hinted at a refresh that had just succeeded", out)
	}
	if query := currentQuery(t, services); query == nil || query.Command != state.CommandDiag {
		t.Errorf("current query = %+v, want the sweep's", query)
	}
}

// A tree is walked again: a namespace's as one, and one resource's from the
// resource at its root.
func TestStaleRefreshWalksTheTreeAgain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query *state.Query
		want  []string
	}{
		{"a namespace", &state.Query{Command: state.CommandTree, Args: []string{"-n", "prod"}},
			[]string{"Namespace/prod", "Deployment/web", "web-abc-1"}},
		{"one resource", &state.Query{Command: state.CommandTree, Resource: "Deployment/web", Args: []string{"-n", "prod"}},
			[]string{"Deployment/web", "web-abc-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleClusterServices(t, &fakeKubectl{namespace: "prod"}, tc.query, treeObjects()...)
			out := runStale(t, services)
			for _, want := range append([]string{"State was stale"}, tc.want...) {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q\n  missing %q", out, want)
				}
			}
			if query := currentQuery(t, services); query == nil || query.Command != state.CommandTree {
				t.Errorf("current query = %+v, want the walk's", query)
			}
		})
	}
}

// A replay that fails names the command that made the listing, not "kx get
// <resource>", which was wrong for every listing kx get did not make.
func TestStaleRefreshNamesTheListingsCommandWhenItCannotRunIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query *state.Query
		want  string
	}{
		{"kx get", &state.Query{Resource: "pods", Args: []string{}}, "Run 'kx get pods -n prod' to refresh the list."},
		{"kx top", &state.Query{Command: state.CommandTop, Resource: "nodes", Args: []string{}}, "Run 'kx top nodes' to refresh the list."},
		{"kx top, narrowed", &state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"-A", "--no-limits"}, Match: matchOf("api")},
			"Run 'kx top -A --no-limits -m api' to refresh the list."},
		{"a sweep", &state.Query{Command: state.CommandDiag, Args: []string{"-A"}, Match: matchOf("api")},
			"Run 'kx diag -A -m api' to refresh the list."},
		// Sorted, its rows were numbered in that order; relisted without
		// the sort, row 3 is another resource.
		{"kx get, sorted", &state.Query{Resource: "pods", Args: []string{}, Sort: "-restarts"},
			"Run 'kx get pods -n prod --sort=-restarts' to refresh the list."},
		{"kx top, sorted", &state.Query{Command: state.CommandTop, Resource: "nodes", Args: []string{}, Sort: "-mem%"},
			"Run 'kx top nodes --sort=-mem%' to refresh the list."},
		{"a sweep, sorted", &state.Query{Command: state.CommandDiag, Args: []string{"-A"}, Sort: "kind", Match: matchOf("api")},
			"Run 'kx diag -A --sort=kind -m api' to refresh the list."},
		{"a namespace's tree", &state.Query{Command: state.CommandTree, Args: []string{"-n", "prod"}},
			"Run 'kx tree -n prod' to refresh the list."},
		// The root itself may be what went: list its kind to find it again,
		// where it was walked — "kx get deployments" typed in default lists
		// default's.
		{"one resource's tree", &state.Query{Command: state.CommandTree, Resource: "Deployment/web", Args: []string{"-n", "prod"}},
			"Run 'kx get deployments -n prod' to refresh the list."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := staleServices(t, &fakeKubectl{err: errors.New("connection refused")}, tc.query)
			services.Kubernetes = func() (kubernetes.Interface, error) {
				return nil, errors.New("connection refused")
			}
			if out := runStale(t, services); !strings.Contains(out, tc.want) {
				t.Errorf("output = %q\n  missing %q", out, tc.want)
			}
		})
	}
}

// A stale -A listing is refreshed under the caption it was listed with. The
// replay took the namespace kx get hands back, which is empty for -A, and so
// captioned "Pods · 2 items" what kx get -A captions "Pods · all namespaces",
// as a refreshed kx top -A already did.
func TestStaleRefreshOfAnAllNamespacesListingSaysSo(t *testing.T) {
	kube := &fakeKubectl{output: "NAMESPACE   NAME      READY   STATUS    RESTARTS   AGE\n" +
		"prod        api-new   1/1     Running   0          1m\n"}
	out := runStale(t, staleServices(t, kube, &state.Query{Resource: "pods", Args: []string{"-A"}}))
	if !strings.Contains(out, "Pods · all namespaces · 1 item") {
		t.Errorf("output = %q, want the refresh captioned all namespaces", out)
	}
}

// A kx get listing that could not be run again is named with its scope, term
// and the flags it was shaped by. Its names are left out — they are what went
// stale — but "kx get pods" for a listing of prod, typed in default, lists
// default.
func TestAFailedGetReplayNamesItsScopeAndTerm(t *testing.T) {
	term := "api"
	for _, tc := range []struct {
		query state.Query
		want  string
	}{
		{state.Query{Resource: "pods", Args: []string{"api-old", "-n", "prod"}, Match: &term},
			"Run 'kx get pods -n prod -m api' to refresh the list."},
		{state.Query{Resource: "pods", Args: []string{"--namespace=prod"}},
			"Run 'kx get pods -n prod' to refresh the list."},
		{state.Query{Resource: "deploy", Args: []string{"-A", "-o", "wide"}},
			"Run 'kx get deploy -A -o wide' to refresh the list."},
	} {
		query := tc.query
		out := runStale(t, staleServices(t, &fakeKubectl{err: errors.New("connection refused")}, &query))
		if !strings.Contains(out, tc.want) {
			t.Errorf("output = %q\n  want %q", out, tc.want)
		}
	}
}

// A stale listing taken without -n is refreshed in the namespace it was taken
// in, not whichever one is current now. kx get deploy in diagnostics, a
// switch to kube-system, and a stale index refreshed into kube-system's
// Deployments — another namespace's listing, saved as current. kx diag and
// kx tree already replay the scope they swept. The query is left as typed,
// so the refresh replaces the stale entry rather than pushing beside it.
func TestAStaleListingIsRefreshedWhereItWasTaken(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  *state.Query
		output string
		call   string
		want   string
	}{
		{"kx get", &state.Query{Resource: "pods", Args: []string{}}, podsOutput,
			"get pods -n prod", "Pods · prod · 2 items"},
		{"kx top", &state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}},
			"NAME      CPU(cores)   MEMORY(bytes)\napi-new   1m           2Mi\n",
			"top pods -n prod", "Pods · prod · 1 item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Current namespace kube-system; the listing was taken in prod.
			kube := &fakeKubectl{output: tc.output, namespace: "kube-system"}
			services := staleServices(t, kube, tc.query)

			out := runStale(t, services)
			if got := joinArgs(kube.calls[len(kube.calls)-1]); got != tc.call {
				t.Errorf("kubectl %q, want %q", got, tc.call)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %q, want %q", out, tc.want)
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

// The command a failed replay names lists the same namespace: "kx get pods"
// for a listing of prod, typed in kube-system, lists kube-system.
func TestAFailedReplayNamesTheNamespaceTheListingWasTakenIn(t *testing.T) {
	for _, tc := range []struct {
		query *state.Query
		want  string
	}{
		{&state.Query{Resource: "pods", Args: []string{}}, "Run 'kx get pods -n prod' to refresh the list."},
		{&state.Query{Command: state.CommandTop, Resource: "pods", Args: []string{"--no-limits"}},
			"Run 'kx top --no-limits -n prod' to refresh the list."},
	} {
		kube := &fakeKubectl{err: errors.New("connection refused"), namespace: "kube-system"}
		if out := runStale(t, staleServices(t, kube, tc.query)); !strings.Contains(out, tc.want) {
			t.Errorf("output = %q\n  want %q", out, tc.want)
		}
	}
}

// A saved kx get all --sort restarts listing whose Pods have all gone came
// back with no table holding RESTARTS, and the replay refused the key it was
// saved with, so the refresh fell back to a hint. The replay drops a key no
// table has now, and keeps it in the query for the next one; the control is
// the same reply typed fresh, which is still refused.
func TestStaleRefreshKeepsASortedListingWhoseColumnHasGone(t *testing.T) {
	const reply = "NAME           TYPE        CLUSTER-IP\nservice/idle   ClusterIP   10.96.0.1\n\n" +
		"NAME                   READY   UP-TO-DATE\ndeployment.apps/idle   0/0     0\n"
	query := &state.Query{Resource: "all", Args: []string{"-n", "prod"}, Sort: "restarts"}
	services := staleServices(t, &fakeKubectl{output: reply}, query)
	out := runStale(t, services)
	if !strings.Contains(out, "State was stale") || !strings.Contains(out, "service/idle") {
		t.Errorf("output = %q, want the refreshed listing", out)
	}
	if got := currentQuery(t, services); got == nil || got.Sort != "restarts" {
		t.Errorf("saved query = %+v, want its --sort kept", got)
	}

	err := executeArgv(newGetCommand(switchServices(t, &fakeKubectl{output: reply, namespace: "prod"})),
		[]string{"all", "--sort", "restarts"})
	want := "Invalid value for '--sort': 'restarts'. Accepted values: name, type, cluster-ip, ready, up-to-date."
	if err == nil || err.Error() != want {
		t.Errorf("kx get all --sort restarts: err = %v, want %q", err, want)
	}
}

// A relist hint is a line to paste back, so a value holding a space is
// quoted. `kx get pods --sort "nominated node"` hinted `kx get pods -n prod
// --sort=nominated node`, which pasted back looked up a Pod named node and
// failed; a -m term read the same way. Double quotes, since the hint around
// the command is in single ones. The control is a one-word value, which is
// still named plainly.
func TestARelistHintQuotesAValueHoldingASpace(t *testing.T) {
	const wide = "NAME    READY   STATUS    RESTARTS   AGE   NOMINATED NODE\n" +
		"web-b   1/1     Running   0          5d    node-2\n" +
		"api     1/1     Running   0          3h    <none>\n"
	relist := func(t *testing.T, argv ...string) string {
		t.Helper()
		quietRender(t)
		services := switchServices(t, &fakeKubectl{output: wide, namespace: "prod"})
		if err := executeArgv(newGetCommand(services), argv); err != nil {
			t.Fatalf("kx get %s: %v", strings.Join(argv, " "), err)
		}
		return relistCommand(currentEntry(t, services), false)
	}

	got := relist(t, "pods", "--sort", "nominated node")
	if want := `kx get pods -n prod --sort="nominated node"`; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}
	got = relist(t, "pods", "--sort", "restarts")
	if want := "kx get pods -n prod --sort=restarts"; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}

	term := "web b"
	entry := state.State{Namespace: "prod", Query: &state.Query{Resource: "pods", Match: &term}}
	if got, want := relistCommand(entry, false), `kx get pods -n prod -m "web b"`; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}
}

// shapedKubectl answers as kubectl does — the wide table under -o wide, the
// default one without it — so a hint run without the shape its listing was
// taken in is read off the columns kubectl would really have printed.
type shapedKubectl struct {
	*fakeKubectl
	wide, plain string
}

func (s *shapedKubectl) Run(args []string) (string, error) {
	s.fakeKubectl.output = s.plain
	if strings.Contains(strings.Join(args, " "), "--output=wide") {
		s.fakeKubectl.output = s.wide
	}
	return s.fakeKubectl.Run(args)
}

// A relist hint names the shape the listing was taken in, since its sort was
// read off the columns that shape printed. `kx get pods -o wide --sort node`
// hinted `kx get pods -n prod --sort=node`, which run was refused — NODE is
// a column of -o wide alone — so the hint named no way back. The control is
// a fetch by index, whose arguments are the rows that went stale and are
// still left out.
func TestARelistHintNamesTheShapeTheListingWasTakenIn(t *testing.T) {
	const wide = "NAME    READY   STATUS    RESTARTS   AGE   IP           NODE\n" +
		"web-b   1/1     Running   0          5d    10.0.0.11    node-2\n" +
		"api     1/1     Running   0          3h    10.0.0.12    node-1\n"
	const plain = "NAME    READY   STATUS    RESTARTS   AGE\n" +
		"web-b   1/1     Running   0          5d\n" +
		"api     1/1     Running   0          3h\n"
	shaped := func() *shapedKubectl {
		return &shapedKubectl{fakeKubectl: &fakeKubectl{namespace: "prod"}, wide: wide, plain: plain}
	}
	quietRender(t)
	services := switchServices(t, shaped())
	if err := executeArgv(newGetCommand(services), []string{"pods", "-o", "wide", "--sort", "node"}); err != nil {
		t.Fatalf("kx get pods -o wide --sort node: %v", err)
	}
	hint := relistCommand(currentEntry(t, services), false)
	if want := "kx get pods -n prod --output=wide --sort=node"; hint != want {
		t.Errorf("relist = %q, want %q", hint, want)
	}
	// The hint as a command: run against the shape it names, its sort reads
	// a column that is there.
	if err := executeArgv(newGetCommand(switchServices(t, shaped())), strings.Fields(hint)[2:]); err != nil {
		t.Errorf("%s: %v", hint, err)
	}

	if err := executeArgv(newGetCommand(services), []string{"pods", "1", "2"}); err != nil {
		t.Fatalf("kx get pods 1 2: %v", err)
	}
	if got, want := relistCommand(currentEntry(t, services), false), "kx get pods -n prod"; got != want {
		t.Errorf("relist of a fetch = %q, want %q", got, want)
	}
}
