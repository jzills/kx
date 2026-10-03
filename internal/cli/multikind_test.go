package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
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

// Indexes of different kinds have no one kind to fetch them as, and kubectl
// will not take a list of kinds beside names, so each kind's own command is
// named instead.
func TestGetSeveralKindsByIndexAcrossKindsIsRefused(t *testing.T) {
	kube := &fakeKubectl{output: deploySvcOutput, namespace: "prod"}
	services := switchServices(t, kube)
	if err := runGet(services, "deploy,svc", nil, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	err := runGet(services, "deploy,svc", []string{"1", "3"}, getOptions{})
	if err == nil {
		t.Fatal("fetched indexes of two kinds as one")
	}
	for _, want := range []string{"Deployment", "Service", "kx get deployments 1", "kx get services 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q\n  missing %q", err, want)
		}
	}
	if len(kube.calls) != 1 {
		t.Errorf("kubectl ran %d times, want the refusal before any fetch", len(kube.calls))
	}
}
