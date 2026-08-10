package discovery

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/kinds"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sdiscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
)

// recordingTransport fails every request instantly and records whether it
// was ever invoked — the test's way of proving "no real network dial was
// even attempted" versus "the delegate was reached and correctly refused."
type recordingTransport struct{ called bool }

func (t *recordingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.called = true
	return nil, errors.New("network disabled in this test")
}

// writeFixtureResourceList writes a serverresources.json cache file in the
// exact encoding CachedDiscoveryClient itself writes (runtime.Encode via
// scheme.Codecs.LegacyCodec), so the real reader can decode it back.
func writeFixtureResourceList(t *testing.T, perHostCacheDir, groupVersion string, list *metav1.APIResourceList) {
	t.Helper()
	list.TypeMeta = metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}
	encoded, err := runtime.Encode(scheme.Codecs.LegacyCodec(), list)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	path := filepath.Join(perHostCacheDir, groupVersion, "serverresources.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// writeFixtureKubeconfig writes the minimal valid kubeconfig
// genericclioptions.ConfigFlags needs to resolve a Host without touching a
// real cluster.
func writeFixtureKubeconfig(t *testing.T, path, host string) {
	t.Helper()
	contents := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"- name: test\n" +
		"  cluster:\n" +
		"    server: " + host + "\n" +
		"    insecure-skip-tls-verify: true\n" +
		"contexts:\n" +
		"- name: test\n" +
		"  context:\n" +
		"    cluster: test\n" +
		"    user: test\n" +
		"current-context: test\n" +
		"users:\n" +
		"- name: test\n" +
		"  user: {}\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// setupFixtureEnv points KUBECONFIG and KUBECACHEDIR at a throwaway
// kubeconfig and cache root, so these tests never touch the machine's real
// ~/.kube. Returns the per-host cache directory ConfigFlags will actually
// read from, computed the same way it does internally
// (<KUBECACHEDIR>/discovery/<sanitized-host>) — tests need this to know
// where to write fixture files, without this package having its own copy
// of that computation (it doesn't; only cli-runtime does).
func setupFixtureEnv(t *testing.T, host string) (perHostCacheDir string) {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("KUBECACHEDIR", cacheRoot)

	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	writeFixtureKubeconfig(t, kubeconfigPath, host)
	t.Setenv("KUBECONFIG", kubeconfigPath)

	// Mirrors cli-runtime's own (unexported) computeDiscoverCacheDir:
	// strip the scheme, replace anything outside [\w/.] with "_". Written
	// here only because the test needs to place a fixture file at the same
	// path the real, non-test code will independently arrive at — this is
	// not production logic and must not be promoted into discovery.go.
	schemeless := host
	for _, prefix := range []string{"https://", "http://"} {
		if len(schemeless) >= len(prefix) && schemeless[:len(prefix)] == prefix {
			schemeless = schemeless[len(prefix):]
			break
		}
	}
	safeHost := ""
	for _, r := range schemeless {
		if r == '_' || r == '.' || r == '/' || (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			safeHost += string(r)
		} else {
			safeHost += "_"
		}
	}
	return filepath.Join(cacheRoot, "discovery", safeHost)
}

func TestNewDiscoveryClientServesFreshFileWithoutTouchingTheNetwork(t *testing.T) {
	host := "https://127.0.0.1:6443"
	perHostCacheDir := setupFixtureEnv(t, host)
	writeFixtureResourceList(t, perHostCacheDir, "v1", &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod"}},
	})

	client, err := newDiscoveryClient()
	if err != nil {
		t.Fatalf("newDiscoveryClient: %v", err)
	}
	list, err := client.ServerResourcesForGroupVersion("v1")
	if err != nil {
		t.Fatalf("ServerResourcesForGroupVersion: %v", err)
	}
	if len(list.APIResources) != 1 || list.APIResources[0].Name != "pods" {
		t.Errorf("resources = %+v, want the cached fixture", list.APIResources)
	}
}

func TestNewDiscoveryClientFailsInstantlyWhenCacheIsMissing(t *testing.T) {
	setupFixtureEnv(t, "https://127.0.0.1:6443") // no fixture file written

	client, err := newDiscoveryClient()
	if err != nil {
		t.Fatalf("newDiscoveryClient: %v", err)
	}
	if _, err := client.ServerResourcesForGroupVersion("v1"); err == nil {
		t.Fatal("expected an error for a missing cache entry")
	}
}

func TestBuildLookupMapsEveryShortNamePluralAndKindToTheSameKind(t *testing.T) {
	lists := []*metav1.APIResourceList{{
		APIResources: []metav1.APIResource{
			{Name: "gateways", SingularName: "gateway", Kind: "Gateway", ShortNames: []string{"gw"}},
		},
	}}
	shorthands, plurals, ok := buildLookup(lists, nil)
	if !ok {
		t.Fatal("buildLookup reported not ok for valid input")
	}
	for _, spelling := range []string{"gw", "gateway", "gateways", "Gateway"} {
		if got := shorthands[strings.ToLower(spelling)]; got != kinds.Kind("Gateway") {
			t.Errorf("shorthands[%q] = %q, want Gateway", spelling, got)
		}
	}
	if plurals[kinds.Kind("Gateway")] != "gateways" {
		t.Errorf("plurals[Gateway] = %q, want gateways", plurals[kinds.Kind("Gateway")])
	}
}

// A subresource (e.g. "deployments/scale") has a "/" in its Name and must
// not pollute the lookup — it is not a spelling anyone would type.
func TestBuildLookupSkipsSubresources(t *testing.T) {
	lists := []*metav1.APIResourceList{{
		APIResources: []metav1.APIResource{
			{Name: "deployments/scale", Kind: "Scale"},
		},
	}}
	shorthands, _, ok := buildLookup(lists, nil)
	if !ok {
		t.Fatal("buildLookup reported not ok for valid input")
	}
	if _, found := shorthands["scale"]; found {
		t.Error("a subresource's Kind leaked into the lookup")
	}
	if len(shorthands) != 0 {
		t.Errorf("shorthands = %v, want empty", shorthands)
	}
}

// A partial-group-discovery failure must not blind the lookup for the
// groups that DID resolve — only some CRD group being uncached shouldn't
// cost every already-cached built-in group its shorthands.
func TestBuildLookupTreatsPartialGroupFailureAsUsable(t *testing.T) {
	lists := []*metav1.APIResourceList{{
		APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", ShortNames: []string{"po"}},
		},
	}}
	partialErr := &k8sdiscovery.ErrGroupDiscoveryFailed{
		Groups: map[schema.GroupVersion]error{{Group: "gateway.networking.k8s.io", Version: "v1"}: errNetworkDisabled},
	}
	shorthands, _, ok := buildLookup(lists, partialErr)
	if !ok {
		t.Fatal("buildLookup treated a partial-group-discovery failure as a hard failure")
	}
	if shorthands["po"] != kinds.Kind("Pod") {
		t.Errorf("shorthands[po] = %q, want Pod even though another group failed", shorthands["po"])
	}
}

func TestBuildLookupHardFailsOnAnyOtherError(t *testing.T) {
	_, _, ok := buildLookup(nil, errNetworkDisabled)
	if ok {
		t.Error("buildLookup reported ok for a non-partial error")
	}
}
