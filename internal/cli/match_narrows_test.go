package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/render"
)

// The rule every --match obeys: what kx prints is narrowed by the term, or
// the command is refused. A term that narrowed nothing and said nothing is
// the failure errMatchBesideIndex already refuses beside an index — it looks
// as though it had checked something. kx get pods -m web -o name | xargs
// kubectl delete deleted every pod in the namespace: the term was applied
// only where kx read kubectl's reply as a table, and every other shape
// passed through untouched.
//
// Each case names a shape kx prints, with kubectl's reply in it; nginx is
// the row the term must leave out, redis the one it keeps.
func TestMatchNarrowsWhatIsPrintedOrRefuses(t *testing.T) {
	const nameReply = "pod/nginx-abc-xyz\npod/redis-def-uvw\n"
	for _, tc := range []struct {
		name    string
		command string
		args    []string
		replies []string
		// refused is a fragment of the refusal, or "" for a narrowed reply.
		refused string
		// preflight refusals come before kubectl is asked for anything.
		preflight bool
	}{
		{name: "-o name", command: "get", args: []string{"pods", "-m", "redis", "-o", "name"},
			replies: []string{nameReply}},
		{name: "--output=name", command: "get", args: []string{"pods", "-m", "redis", "--output=name"},
			replies: []string{nameReply}},
		{name: "kx secret -o name", command: "secret", args: []string{"-m", "redis", "-o", "name"},
			replies: []string{"secret/nginx-tls\nsecret/redis-auth\n"}},
		{name: "another cluster's -o name", command: "get",
			args: []string{"pods", "--context=b", "-m", "redis", "-o", "name"}, replies: []string{nameReply}},
		{name: "-o json", command: "get", args: []string{"pods", "-m", "redis", "-o", "json"},
			refused: "-o json", preflight: true},
		{name: "-o yaml", command: "get", args: []string{"pods", "-m", "redis", "-oyaml"},
			refused: "-o yaml", preflight: true},
		{name: "-o jsonpath", command: "get",
			args:    []string{"pods", "-m", "redis", "-o", "jsonpath={.items[*].metadata.name}"},
			refused: "-o jsonpath", preflight: true},
		{name: "-o go-template", command: "get",
			args:    []string{"pods", "-m", "redis", "-o=go-template={{range .items}}{{.metadata.name}}{{end}}"},
			refused: "-o go-template", preflight: true},
		{name: "another cluster's -o json", command: "get",
			args: []string{"pods", "--context=b", "-m", "redis", "-o", "json"}, refused: "-o json", preflight: true},
		{name: "custom columns with no NAME", command: "get",
			args:    []string{"pods", "-m", "redis", "-o", "custom-columns=POD:.metadata.name"},
			replies: []string{"POD\nnginx-abc-xyz\nredis-def-uvw\n"}, refused: "NAME column"},
		{name: "--no-headers", command: "get", args: []string{"pods", "-m", "redis", "--no-headers"},
			replies: []string{"nginx-abc-xyz   1/1   Running   0     5d\nredis-def-uvw   1/1   Running   0     3d\n"},
			refused: "--no-headers"},
		{name: "a watch kx streams", command: "get", args: []string{"pods", "-m", "redis", "-w", "-o", "name"},
			refused: "--watch", preflight: true},
		{name: "contexts", command: "get", args: []string{"contexts", "-m", "redis"},
			replies: []string{"CURRENT   NAME        CLUSTER\n*         nginx-ctx   a\n          redis-ctx   b\n"}},
		{name: "kx top --no-headers", command: "top", args: []string{"-m", "redis", "--no-headers"},
			replies: []string{"nginx-abc-xyz   1m   10Mi\nredis-def-uvw   2m   20Mi\n"}, refused: "--no-headers"},
		{name: "another cluster's kx top --no-headers", command: "top",
			args:    []string{"--context=b", "-m", "redis", "--no-headers"},
			replies: []string{"nginx-abc-xyz   1m   10Mi\nredis-def-uvw   2m   20Mi\n"}, refused: "--no-headers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kube := &fakeKubectl{outputs: tc.replies, namespace: "prod"}
			services := switchServices(t, kube)
			cmd := newGetCommand(services)
			switch tc.command {
			case "secret":
				cmd = newSecretCommand(services, "secret", nil)
			case "top":
				cmd = newTopCommand(services)
			}
			stdout, stderr, err := runCaptured(t, cmd, tc.args)

			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), "--match") || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("err = %v, want a refusal of --match naming %q; stdout %q", err, tc.refused, stdout)
				}
				if strings.Contains(stdout, "nginx") || strings.Contains(stdout, "redis") {
					t.Errorf("stdout = %q, want nothing printed beside the refusal", stdout)
				}
				if tc.preflight && len(kube.calls) != 0 {
					t.Errorf("kubectl was run %q before a refusal its arguments already decided", kube.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; stderr %q", err, stderr)
			}
			if !strings.Contains(stdout, "redis") || strings.Contains(stdout, "nginx") {
				t.Errorf("stdout = %q, want redis kept and nginx narrowed away", stdout)
			}
		})
	}
}

// A term that narrows -o name to nothing prints nothing a program reading
// the names would take for one, and says so on stderr, as an empty reply in
// that format does.
func TestMatchNarrowingNamesToNothingPrintsNone(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{"pod/nginx-abc-xyz\n"}, namespace: "prod"}
	services := switchServices(t, kube)
	stdout, stderr, err := runCaptured(t, newGetCommand(services), []string{"pods", "-m", "redis", "-o", "name"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q, want no names", stdout)
	}
	if !strings.Contains(stderr, "nothing matches 'redis'") {
		t.Errorf("stderr = %q, want the term named", stderr)
	}
}

// Fetched across namespaces — one kubectl call each — the replies in -o name
// are narrowed together.
func TestMatchNarrowsNamesFetchedAcrossNamespaces(t *testing.T) {
	kube := &fakeKubectl{outputs: []string{
		"NAMESPACE   NAME            READY   STATUS    RESTARTS   AGE\n" +
			"prod        nginx-abc-xyz   1/1     Running   0          5d\n" +
			"stage       redis-def-uvw   1/1     Running   0          3d\n",
		"pod/nginx-abc-xyz\n",
		"pod/redis-def-uvw\n",
	}, namespace: "prod"}
	services := switchServices(t, kube)
	quietRender(t)
	if err := runGet(services, "pods", []string{"-A"}, getOptions{}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	stdout, _, err := runCaptured(t, newGetCommand(services), []string{"pods", "1", "2", "-m", "redis", "-o", "name"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stdout, "pod/redis-def-uvw") || strings.Contains(stdout, "nginx") {
		t.Errorf("stdout = %q, want redis kept and nginx narrowed away", stdout)
	}
}

// A context named by index is switched to, and a term beside it has nothing
// left to narrow, so it is refused as it is beside any index.
func TestMatchBesideAContextIndexIsRefused(t *testing.T) {
	kube := &fakeKubectl{namespace: "prod"}
	services := switchServices(t, kube)
	_, _, err := runCaptured(t, newGetCommand(services), []string{"contexts", "1", "-m", "redis"})
	if err == nil || err.Error() != errMatchBesideContextIndex.Error() {
		t.Fatalf("err = %v, want %v", err, errMatchBesideContextIndex)
	}
	if len(kube.calls) != 0 {
		t.Errorf("kubectl was run %q before the refusal", kube.calls)
	}
}

// terminalBuffer is a buffer render takes for a terminal, so a test sees
// what kx draws on one: the live watch draws nothing anywhere else.
type terminalBuffer struct{ bytes.Buffer }

func (*terminalBuffer) IsTerminal() bool { return true }

// The live table kx draws for a watch is narrowed by the term, as the
// listing it redraws would be: kx get pods -w -m web watched every pod.
func TestMatchNarrowsTheWatchKxDraws(t *testing.T) {
	kube := &fakeKubectl{namespace: "prod", watchLines: []string{
		"EVENT    NAME            READY   STATUS    RESTARTS   AGE",
		"ADDED    nginx-abc-xyz   1/1     Running   0          5d",
		"ADDED    redis-def-uvw   1/1     Running   0          3d",
	}}
	services := switchServices(t, kube)
	var out terminalBuffer
	var errOut bytes.Buffer
	render.SetOutput(&out, &errOut, "github-dark")
	cmd := newGetCommand(services)
	cmd.SetArgs([]string{"pods", "-m", "redis", "-w"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("err = %v; stderr %q", err, errOut.String())
	}
	if !strings.Contains(out.String(), "redis-def-uvw") {
		t.Fatalf("stdout = %q, want the watch drawn with redis in it", out.String())
	}
	if strings.Contains(out.String(), "nginx") {
		t.Errorf("stdout = %q, want nginx narrowed away", out.String())
	}
}

// -o name is narrowed by each line's name, not the kind in front of it, as a
// table is (index.FilterRows): "app" is in every "deployment.apps/…".
func TestNarrowNamesMatchesTheNameNotTheKind(t *testing.T) {
	output := "deployment.apps/web\ndeployment.apps/api-gateway\nservice/api\n"
	if got := narrowNames(output, "app"); got != "" {
		t.Errorf("'app' kept %q through the apps group in the kind prefix", got)
	}
	if got := narrowNames(output, "API"); got != "deployment.apps/api-gateway\nservice/api" {
		t.Errorf("'API' kept %q, want both resources named api, case-insensitively", got)
	}
}
