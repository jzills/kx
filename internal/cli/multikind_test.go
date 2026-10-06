package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// What `kubectl get deploy,svc` prints: a table per kind, every name
// carrying its kind.
const deploySvcOutput = "NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
	"deployment.apps/api   1/1     1            1           5d\n" +
	"deployment.apps/web   2/2     2            2           3d\n" +
	"\n" +
	"NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
	"service/api   ClusterIP   10.0.0.11    <none>        80/TCP    5d\n"

const singlePodOutput = "NAME            READY   STATUS    RESTARTS   AGE\n" +
	"nginx-abc-xyz   1/1     Running   0          5d\n"

// A listing of several kinds saved every row under the argument it was asked
// with — kind "deploy,svc", name "deployment.apps/api" — so every index
// resolved to deploy,svc/deployment.apps/api, which kubectl rejects, and kx
// then reported a resource that exists as one that no longer does. Each row
// is saved as the kind kubectl printed in front of it.
func TestGetSeveralKindsSavesEachRowAsItsOwnKind(t *testing.T) {
	for _, resource := range []string{"deploy,svc", "all"} {
		t.Run(resource, func(t *testing.T) {
			kube := &fakeKubectl{output: deploySvcOutput, namespace: "prod"}
			services := switchServices(t, kube)
			var out bytes.Buffer
			render.SetOutput(&out, &out, "github-dark")
			if err := runGet(services, resource, nil, getOptions{}); err != nil {
				t.Fatalf("runGet: %v", err)
			}

			for i, want := range []struct {
				name string
				kind kinds.Kind
			}{{"api", kinds.Deployment}, {"web", kinds.Deployment}, {"api", kinds.Service}} {
				name, namespace, kind, err := services.State.Fields(i + 1)
				if err != nil || name != want.name || kind != want.kind || namespace != "prod" {
					t.Errorf("index %d = %s/%s in %s (err %v), want %s/%s in prod",
						i+1, kind, name, namespace, err, want.kind, want.name)
				}
			}
			if _, _, _, err := services.State.Fields(4); err == nil {
				t.Error("index 4 resolves; the Service table's header was saved as a row")
			}
			// Mixed, as kx state captions the same entry, rather than the
			// argument as typed.
			if !strings.Contains(out.String(), "Mixed · prod · 3 items") {
				t.Errorf("output = %q, want a Mixed caption over 3 items", out.String())
			}
			if !strings.Contains(out.String(), "CLUSTER-IP") {
				t.Errorf("output = %q, want the Service table under its own header", out.String())
			}
		})
	}
}

// kx get pod/<name> asks for one kind by name, and kubectl prints the name
// bare. The kind was saved as "pod/<name>", so the index resolved to
// pod/<name>/<name>.
func TestGetTypeAndNameSavesTheType(t *testing.T) {
	kube := &fakeKubectl{output: singlePodOutput, namespace: "prod"}
	services := switchServices(t, kube)
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "pod/nginx-abc-xyz", nil, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	name, _, kind, err := services.State.Fields(1)
	if err != nil || name != "nginx-abc-xyz" || kind != kinds.Pod {
		t.Errorf("index 1 = %s/%s (err %v), want Pod/nginx-abc-xyz", kind, name, err)
	}
	if !strings.HasPrefix(out.String(), "Pods · prod · 1 item") {
		t.Errorf("output = %q, want it captioned as Pods", out.String())
	}
}

// A table of several kinds whose names carry no kind — custom columns — has
// nothing to resolve a row's kind from, so it is printed as kubectl gave it
// and saved as nothing, as kx treats any output it cannot number.
func TestGetSeveralKindsWithoutKindsInTheNamesIsNotNumbered(t *testing.T) {
	output := "NAME\napi\nweb\n\nNAME\napi\n"
	kube := &fakeKubectl{output: output, namespace: "prod"}
	services := switchServices(t, kube)
	saveListing(t, services, kinds.Pod, "prod", false, "nginx")
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "deploy,svc", []string{"-o", "custom-columns=NAME:.metadata.name"}, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	if out.String() != output+"\n" {
		t.Errorf("output = %q, want kubectl's own %q", out.String(), output)
	}
	if name, _, _, err := services.State.Fields(1); err != nil || name != "nginx" {
		t.Errorf("index 1 = %q (err %v), want the listing before it still current", name, err)
	}
}

// An index into a listing of several kinds is fetched again as the kind it
// is: kx get all 2 is kx get deployment 2. Checked against "all" itself, it
// was refused as "Index 2 is Deployment/web, not all — run 'kx get mixed'",
// naming a command that does not exist.
func TestGetSeveralKindsByIndexFetchesTheRowsOwnKind(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{
		deploySvcOutput,
		"NAME   READY   UP-TO-DATE   AVAILABLE   AGE\nweb    2/2     2            2           3d\n",
	}, namespace: "prod"}
	services := switchServices(t, kube)
	if err := runGet(services, "all", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	if err := runGet(services, "all", []string{"2"}, getOptions{}); err != nil {
		t.Fatalf("kx get all 2: %v", err)
	}
	if got, want := joinArgs(kube.calls[1]), "get Deployment web -n prod"; got != want {
		t.Errorf("kubectl %q, want %q", got, want)
	}
	name, _, kind, err := services.State.Fields(1)
	if err != nil || name != "web" || kind != kinds.Deployment {
		t.Errorf("index 1 = %s/%s (err %v), want Deployment/web", kind, name, err)
	}
}

// Indexes of different kinds are fetched together, each named Kind/name,
// which kubectl takes in one call where it takes no list of kinds beside
// names. They were refused with a command per kind, and the first of those
// saved a listing of its own rows, so the second's index no longer meant what
// it had: following the advice as written failed.
func TestGetSeveralKindsByIndexAcrossKindsFetchesThemTogether(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{deploySvcOutput, deploySvcOutput}, namespace: "prod"}
	services := switchServices(t, kube)
	if err := runGet(services, "deploy,svc", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "deploy,svc", []string{"1", "3"}, getOptions{}); err != nil {
		t.Fatalf("kx get deploy,svc 1 3: %v", err)
	}
	if got, want := joinArgs(kube.calls[1]), "get Deployment/api Service/api --show-kind -n prod"; got != want {
		t.Errorf("kubectl %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), "Mixed · prod") || !strings.Contains(out.String(), "CLUSTER-IP") {
		t.Errorf("output = %q, want a Mixed listing with each kind under its own header", out.String())
	}
	// The fetch is a listing like any other: its rows are the next indexes,
	// each the kind kubectl printed in front of it.
	for i, want := range []struct {
		name string
		kind kinds.Kind
	}{{"api", kinds.Deployment}, {"web", kinds.Deployment}, {"api", kinds.Service}} {
		name, _, kind, err := services.State.Fields(i + 1)
		if err != nil || name != want.name || kind != want.kind {
			t.Errorf("index %d = %s/%s (err %v), want %s/%s", i+1, kind, name, err, want.kind, want.name)
		}
	}
}

// --show-kind is what keeps every row naming its kind: kubectl prefixes names
// only when one reply holds several kinds, and an -A listing's indexes are
// fetched a namespace at a time, so a namespace holding one of them answers
// with a bare name. Each kind keeps its own table across the namespaces, under
// a NAMESPACE column, rather than every row sitting under the first table's
// columns.
func TestGetSeveralKindsByIndexAcrossNamespacesKeepsEachKindsTable(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{
		"NAMESPACE   NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
			"prod        deployment.apps/api   1/1     1            1           5d\n" +
			"\n" +
			"NAMESPACE   NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
			"stage       service/web   ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
		"NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
			"deployment.apps/api   1/1     1            1           5d\n",
		"NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
			"service/web   ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
	}, namespace: "prod"}
	services := switchServices(t, kube)
	if err := runGet(services, "all", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "all", []string{"1", "2"}, getOptions{}); err != nil {
		t.Fatalf("kx get all 1 2: %v", err)
	}
	for i, want := range []string{
		"get Deployment/api -n prod --show-kind", "get Service/web -n stage --show-kind",
	} {
		if got := joinArgs(kube.calls[i+1]); got != want {
			t.Errorf("call %d = %q, want %q", i+1, got, want)
		}
	}
	if !strings.Contains(out.String(), "CLUSTER-IP") || strings.Count(out.String(), "NAMESPACE") != 2 {
		t.Errorf("output = %q, want each kind's table under its own header", out.String())
	}
	for i, want := range []struct {
		name, namespace string
		kind            kinds.Kind
	}{{"api", "prod", kinds.Deployment}, {"web", "stage", kinds.Service}} {
		name, namespace, kind, err := services.State.Fields(i + 1)
		if err != nil || name != want.name || kind != want.kind || namespace != want.namespace {
			t.Errorf("index %d = %s/%s in %s (err %v), want %s/%s in %s",
				i+1, kind, name, namespace, err, want.kind, want.name, want.namespace)
		}
	}
}

// A fetch of rows spanning kinds is refreshed by running that fetch again,
// captioned Mixed as it was first. kubectl is given no resource beside rows
// named Kind/name, but the fetch is recorded under the one it was asked
// with, so a replay that fails names the listing to run instead.
func TestAStaleFetchAcrossKindsIsRefreshedAsOne(t *testing.T) {
	query := &state.Query{Resource: "deploy,svc", Args: []string{"Deployment/api", "Service/api", "--show-kind", "-n", "prod"}}
	kube := &fakeKubectl{output: deploySvcOutput, namespace: "prod"}
	out := runStale(t, staleServices(t, kube, query))
	if got, want := joinArgs(kube.calls[0]), "get Deployment/api Service/api --show-kind -n prod"; got != want {
		t.Errorf("replayed %q, want %q", got, want)
	}
	if !strings.Contains(out, "Mixed · prod · 3 items") {
		t.Errorf("output = %q, want the refresh captioned Mixed", out)
	}

	failing := &fakeKubectl{err: errors.New("connection refused")}
	out = runStale(t, staleServices(t, failing, query))
	if !strings.Contains(out, "Run 'kx get deploy,svc -n prod' to refresh the list.") {
		t.Errorf("output = %q, want the listing the rows came from", out)
	}
}

// A fetch of rows spanning kinds is recorded as the listing it was asked for:
// kx get deploy,svc 1 3 is a deploy,svc listing. Recorded with no resource,
// a stale index into it named no command to relist — "kx get <resource>" —
// and kx state <TAB> labelled one spanning namespaces "fetch", a command
// nobody types.
func TestGetSeveralKindsByIndexRecordsTheListingAskedFor(t *testing.T) {
	t.Run("one namespace", func(t *testing.T) {
		kube := &fakeKubectl{outputs: []string{deploySvcOutput, deploySvcOutput}, namespace: "prod"}
		services := switchServices(t, kube)
		quietRender(t)
		if err := runGet(services, "deploy,svc", nil, getOptions{}); err != nil {
			t.Fatalf("seed listing: %v", err)
		}
		if err := runGet(services, "deploy,svc", []string{"1", "3"}, getOptions{}); err != nil {
			t.Fatalf("kx get deploy,svc 1 3: %v", err)
		}
		if got, want := relistCommand(currentEntry(t, services)), "kx get deploy,svc -n prod"; got != want {
			t.Errorf("relist = %q, want %q", got, want)
		}
		if candidates := completePosition(services, ""); candidates[len(candidates)-1] != "2\tdeploy,svc in prod" {
			t.Errorf("candidates = %q, want the fetch labelled \"deploy,svc in prod\"", candidates)
		}
	})
	t.Run("across namespaces", func(t *testing.T) {
		kube := &fakeKubectl{outputs: []string{
			"NAMESPACE   NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
				"prod        deployment.apps/api   1/1     1            1           5d\n" +
				"\n" +
				"NAMESPACE   NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
				"stage       service/web   ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
			"NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
				"deployment.apps/api   1/1     1            1           5d\n",
			"NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
				"service/web   ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
		}, namespace: "prod"}
		services := switchServices(t, kube)
		quietRender(t)
		if err := runGet(services, "all", []string{"-A"}, getOptions{}); err != nil {
			t.Fatalf("seed listing: %v", err)
		}
		if err := runGet(services, "all", []string{"1", "2"}, getOptions{}); err != nil {
			t.Fatalf("kx get all 1 2: %v", err)
		}
		if got, want := relistCommand(currentEntry(t, services)), "kx get all -A"; got != want {
			t.Errorf("relist = %q, want %q", got, want)
		}
		if candidates := completePosition(services, ""); candidates[len(candidates)-1] != "2\tall" {
			t.Errorf("candidates = %q, want the fetch labelled \"all\"", candidates)
		}
	})
}

// Rows fetched across kinds in a shape that leaves the kind off their names —
// custom columns — are printed as kubectl gave them and saved as nothing, as
// kx get deploy,svc -o custom-columns is. Saved, every row's kind was empty,
// and kx describe 1 ran kubectl describe "" and reported a Deployment that
// exists as one that no longer does.
func TestGetSeveralKindsByIndexWithoutKindsInTheNamesIsNotNumbered(t *testing.T) {
	output := "NAME\napi\napi\n"
	kube := &fakeKubectl{outputs: []string{deploySvcOutput, output}, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	if err := runGet(services, "deploy,svc", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "deploy,svc",
		[]string{"1", "3", "-o", "custom-columns=NAME:.metadata.name"}, getOptions{}); err != nil {
		t.Fatalf("kx get deploy,svc 1 3 -o custom-columns: %v", err)
	}
	if out.String() != output+"\n" {
		t.Errorf("output = %q, want kubectl's own %q", out.String(), output)
	}
	name, _, kind, err := services.State.Fields(1)
	if err != nil || name != "api" || kind != kinds.Deployment {
		t.Errorf("index 1 = %s/%s (err %v), want the listing before it still current", kind, name, err)
	}
}

// The same across namespaces, where each namespace is fetched on its own and
// one holding a single kind answers with bare names unless --show-kind is
// in force: --show-kind=false left both rows of kx get all 1 2 with an empty
// kind. They are printed stitched but unnumbered, and the -A listing stays
// current.
func TestGetSeveralKindsByIndexAcrossNamespacesWithoutKindsIsNotNumbered(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{
		"NAMESPACE   NAME                  READY   UP-TO-DATE   AVAILABLE   AGE\n" +
			"prod        deployment.apps/api   1/1     1            1           5d\n" +
			"\n" +
			"NAMESPACE   NAME          TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\n" +
			"stage       service/web   ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
		"NAME   READY   UP-TO-DATE   AVAILABLE   AGE\napi    1/1     1            1           5d\n",
		"NAME   TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE\nweb    ClusterIP   10.0.0.12    <none>        80/TCP    3d\n",
	}, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	if err := runGet(services, "all", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "all", []string{"1", "2", "--show-kind=false"}, getOptions{}); err != nil {
		t.Fatalf("kx get all 1 2 --show-kind=false: %v", err)
	}
	if strings.Contains(out.String(), "Mixed ·") {
		t.Errorf("output = %q, want no caption over rows kx did not number", out.String())
	}
	if !strings.Contains(out.String(), "stage") || !strings.Contains(out.String(), "CLUSTER-IP") {
		t.Errorf("output = %q, want each kind's table, its namespace put back", out.String())
	}
	name, namespace, kind, err := services.State.Fields(1)
	if err != nil || name != "api" || kind != kinds.Deployment || namespace != "prod" {
		t.Errorf("index 1 = %s/%s in %s (err %v), want the -A listing still current", kind, name, namespace, err)
	}
}

// A kind named by its group — kx get deployments.apps — is saved and
// captioned as the kind it is. Saved as "deployments.apps", the rows were
// refused by kx scale and kx rollout as an unsupported kind.
func TestGetAGroupQualifiedKindSavesTheKind(t *testing.T) {
	kube := &fakeKubectl{
		output:    "NAME   READY   UP-TO-DATE   AVAILABLE   AGE\nweb    2/2     2            2           3d\n",
		namespace: "prod",
	}
	services := switchServices(t, kube)
	var out bytes.Buffer
	render.SetOutput(&out, &out, "github-dark")
	if err := runGet(services, "deployments.apps", nil, getOptions{}); err != nil {
		t.Fatalf("runGet: %v", err)
	}
	name, _, kind, err := services.State.Fields(1)
	if err != nil || name != "web" || kind != kinds.Deployment {
		t.Errorf("index 1 = %s/%s (err %v), want Deployment/web", kind, name, err)
	}
	if !strings.HasPrefix(out.String(), "Deployments · prod · 1 item") {
		t.Errorf("output = %q, want it captioned as Deployments", out.String())
	}
	if _, _, err := (ScaleCommand{Kubectl: kube, State: services.State}).Execute(state.Ref{Index: 1}, 3, nil); err != nil {
		t.Errorf("kx scale refused the row: %v", err)
	}
}

// A row is fetched under a resource naming several kinds only when the
// resource lists it. Skipping the check, kx get deploy,svc 2 after kx get pods
// fetched Pod 2 and saved it over the listing, where kx get deploy 2 is
// refused; and kx get all 2 --decode after kx get secrets printed a Secret's
// plaintext, though kubectl's all category holds none.
func TestGetSeveralKindsRefusesARowItDoesNotList(t *testing.T) {
	cases := []struct {
		resource string
		kind     kinds.Kind
		options  getOptions
		want     string
	}{
		{"deploy,svc", kinds.Pod, getOptions{},
			"Index 2 is Pod/web-2, which 'deploy,svc' does not include — run 'kx get deploy,svc' to relist."},
		{"all", kinds.Secret, getOptions{Decode: true},
			"Index 2 is Secret/web-2, which 'all' does not include — run 'kx get all' to relist."},
		{"all", kinds.ConfigMap, getOptions{},
			"Index 2 is ConfigMap/web-2, which 'all' does not include — run 'kx get all' to relist."},
	}
	for _, c := range cases {
		t.Run(c.resource+"/"+string(c.kind), func(t *testing.T) {
			kube := &fakeKubectl{output: singlePodOutput, namespace: "prod"}
			services := switchServices(t, kube)
			saveListing(t, services, c.kind, "prod", false, "web-1", "web-2")
			quietRender(t)
			err := runGet(services, c.resource, []string{"2"}, c.options)
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if len(kube.calls) != 0 {
				t.Errorf("kubectl ran %q; nothing should be fetched", kube.calls)
			}
			if name, _, kind, err := services.State.Fields(2); err != nil || name != "web-2" || kind != c.kind {
				t.Errorf("index 2 = %s/%s (err %v), want the listing left as it was", kind, name, err)
			}
		})
	}
}

// One row the resource does not list refuses the batch, before anything is
// fetched — the same way an out-of-range index late in a batch does.
func TestGetSeveralKindsRefusesABatchHoldingARowItDoesNotList(t *testing.T) {
	kube := &fakeKubectl{output: deploySvcOutput, namespace: "prod"}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewOrderedResources([]state.Resource{
			{Name: "api", Kind: kinds.Deployment}, {Name: "creds", Kind: kinds.Secret},
		}),
		Namespace: "prod",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	quietRender(t)
	err := runGet(services, "all", []string{"1", "2"}, getOptions{})
	if err == nil || !strings.Contains(err.Error(), "Index 2 is Secret/creds, which 'all' does not include") {
		t.Fatalf("err = %v, want index 2 refused", err)
	}
	if len(kube.calls) != 0 {
		t.Errorf("kubectl ran %q; nothing should be fetched", kube.calls)
	}
}

// A mark names its resource whatever is listed, so it is refused in its own
// voice, and pointed at the kind it is.
func TestGetSeveralKindsRefusesAMarkItDoesNotList(t *testing.T) {
	kube := &fakeKubectl{output: singlePodOutput, namespace: "prod"}
	services := switchServices(t, kube)
	if err := services.State.SaveMark("db", state.Mark{
		Resource: state.Resource{Name: "creds", Kind: kinds.Secret, Namespace: "prod"},
	}); err != nil {
		t.Fatalf("SaveMark: %v", err)
	}
	quietRender(t)
	err := runGet(services, "all", []string{"@db"}, getOptions{Decode: true})
	want := "@db is Secret/creds, which 'all' does not include — run 'kx get secrets @db' to fetch it."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}
