package discovery

import (
	"bytes"
	"errors"
	"io"
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
	"k8s.io/klog/v2"
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
// scheme.Codecs.LegacyCodec), so the real reader can decode it back. It also
// registers groupVersion in servergroups.json: CachedDiscoveryClient.
// ServerPreferredResources (which Source.load calls) first reads the group
// list to learn which group/versions exist at all — a serverresources.json
// fixture that isn't also listed there is invisible to it, exactly as it
// would be against a real, incompletely-cached cluster.
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
	registerFixtureGroupVersion(t, perHostCacheDir, groupVersion)
}

// registerFixtureGroupVersion adds groupVersion to servergroups.json,
// merging with whatever is already recorded there. See
// writeFixtureResourceList for why this file has to exist alongside the
// per-group-version fixture.
func registerFixtureGroupVersion(t *testing.T, perHostCacheDir, groupVersion string) {
	t.Helper()
	gv, err := schema.ParseGroupVersion(groupVersion)
	if err != nil {
		t.Fatalf("ParseGroupVersion(%q): %v", groupVersion, err)
	}

	path := filepath.Join(perHostCacheDir, "servergroups.json")
	groups := &metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}}
	if existing, err := os.ReadFile(path); err == nil {
		if err := runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), existing, groups); err != nil {
			t.Fatalf("decode existing servergroups.json: %v", err)
		}
	}

	version := metav1.GroupVersionForDiscovery{GroupVersion: groupVersion, Version: gv.Version}
	found := false
	for i := range groups.Groups {
		if groups.Groups[i].Name == gv.Group {
			groups.Groups[i].Versions = append(groups.Groups[i].Versions, version)
			groups.Groups[i].PreferredVersion = version
			found = true
			break
		}
	}
	if !found {
		groups.Groups = append(groups.Groups, metav1.APIGroup{
			Name:             gv.Group,
			Versions:         []metav1.GroupVersionForDiscovery{version},
			PreferredVersion: version,
		})
	}

	encoded, err := runtime.Encode(scheme.Codecs.LegacyCodec(), groups)
	if err != nil {
		t.Fatalf("encode servergroups.json: %v", err)
	}
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

// writeFixtureKubeconfigWithExecPlugin writes a kubeconfig whose user auths
// via an `exec:` credential plugin that touches markerPath when run — proof,
// for the test, of whether the plugin was ever invoked. Mirrors the `exec:`
// block real EKS/GKE/AKS and kubelogin-style kubeconfigs carry.
func writeFixtureKubeconfigWithExecPlugin(t *testing.T, path, host, markerPath string) {
	t.Helper()
	scriptPath := filepath.Join(filepath.Dir(path), "exec-plugin.sh")
	script := "#!/bin/sh\n" +
		"touch '" + markerPath + "'\n" +
		"echo '{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"kind\":\"ExecCredential\",\"status\":{\"token\":\"fake-token\"}}'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(exec plugin script): %v", err)
	}

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
		"  user:\n" +
		"    exec:\n" +
		"      apiVersion: client.authentication.k8s.io/v1\n" +
		"      command: " + scriptPath + "\n" +
		"      interactiveMode: Never\n"
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

// A cache miss must never invoke the kubeconfig's exec credential plugin:
// rest.Config.TransportConfig() wires ExecProvider into the transport chain
// OUTSIDE (before) WrapTransport, so without clearing it in WrapConfigFn the
// plugin's RoundTrip — which can spawn a process, call out to a cloud IdP, or
// block on an interactive login prompt — would run before refusingTransport
// ever gets a chance to refuse the request.
func TestNewDiscoveryClientNeverInvokesTheExecCredentialPlugin(t *testing.T) {
	host := "https://198.51.100.7:6443" // TEST-NET-2 (RFC 5737): guaranteed unroutable
	cacheRoot := t.TempDir()
	t.Setenv("KUBECACHEDIR", cacheRoot)

	kubeconfigDir := t.TempDir()
	markerPath := filepath.Join(kubeconfigDir, "exec-plugin-ran")
	kubeconfigPath := filepath.Join(kubeconfigDir, "kubeconfig")
	writeFixtureKubeconfigWithExecPlugin(t, kubeconfigPath, host, markerPath)
	t.Setenv("KUBECONFIG", kubeconfigPath)
	// No fixture cache written: this is a cache miss, the exact path that
	// reaches the transport (and, before the fix, the exec plugin) at all.

	client, err := newDiscoveryClient()
	if err != nil {
		t.Fatalf("newDiscoveryClient: %v", err)
	}
	if _, err := client.ServerResourcesForGroupVersion("v1"); err == nil {
		t.Fatal("expected an error for a missing cache entry")
	}

	if _, err := os.Stat(markerPath); err == nil {
		t.Error("exec credential plugin was invoked; want it never run")
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat(markerPath): %v", err)
	}
}

// The in-memory discovery cache reports refusingTransport's error via
// apimachinery's utilruntime.HandleError, which by default logs to klog ->
// stderr. Uninstrumented, that fires on every unrecognized token typed at
// kx; Source.load must suppress it.
func TestSourceLoadNeverLogsTheRefusedTransportError(t *testing.T) {
	setupFixtureEnv(t, "https://198.51.100.7:6443") // no fixture: forces a cache miss

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	klog.SetOutput(w)
	t.Cleanup(func() {
		klog.SetOutput(origStderr)
	})

	source := NewSource()
	source.Resolve("nonexistent-thing")
	klog.Flush()

	os.Stderr = origStderr
	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)

	if strings.Contains(buf.String(), "Unhandled Error") {
		t.Errorf("stderr contained an \"Unhandled Error\" klog line, want none:\n%s", buf.String())
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

// Source.Resolve against a real fixture cache directory — the only test in
// this file that exercises every earlier task's piece together.
func TestSourceResolvesFromAFixtureCache(t *testing.T) {
	host := "https://127.0.0.1:6443"
	perHostCacheDir := setupFixtureEnv(t, host)
	writeFixtureResourceList(t, perHostCacheDir, "v1", &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", ShortNames: []string{"po"}},
		},
	})

	source := NewSource()
	kind, plural, ok := source.Resolve("po")
	if !ok || kind != kinds.Kind("Pod") || plural != "pods" {
		t.Errorf("Resolve(po) = (%q, %q, %v), want (Pod, pods, true)", kind, plural, ok)
	}

	_, _, ok = source.Resolve("nonexistent-thing")
	if ok {
		t.Error("Resolve(nonexistent-thing) = true, want false")
	}
}
