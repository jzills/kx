// Package discovery resolves resource-type shorthands (a CRD's short name,
// plural, or kind) that kx's own static kind map does not know about, by
// reading kubectl's own on-disk API-server discovery cache
// (~/.kube/cache/discovery/..., or $KUBECACHEDIR if set).
//
// kx never calls the API server for this, under any circumstance: a fresh
// cache entry is served straight from disk, and a stale or missing one
// fails instantly via a transport that refuses every dial rather than a
// real network attempt. See internal/kinds.ShorthandSource, which this
// package implements — kx wraps kubectl, and kubectl already populates
// this cache as a side effect of any command that resolves a resource
// type, including the "kubectl get <resource>" kx itself shells out to
// for `kx get`.
package discovery

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/jzills/kx/internal/kinds"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	restclient "k8s.io/client-go/rest"
)

var errNetworkDisabled = errors.New(
	"internal/discovery: network access is intentionally disabled; only kubectl's own cached discovery data is read")

// refusingTransport fails every request instantly. Installed via
// WrapConfigFn so a cache miss (or an entry older than kubectl's own
// 6-hour discovery TTL) degrades immediately rather than attempting a real
// network dial — the entire reason this package never blocks or adds
// latency to a command that doesn't need it.
type refusingTransport struct{}

func (refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errNetworkDisabled
}

// newDiscoveryClient builds a read-only, network-refusing discovery client
// against the ambient kubeconfig. genericclioptions.ConfigFlags supplies
// the real cache-directory computation, kubeconfig loading, and
// cache-reading kubectl itself uses — this package does not reimplement
// any of it.
func newDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	flags := genericclioptions.NewConfigFlags(true)
	flags.WrapConfigFn = func(config *restclient.Config) *restclient.Config {
		// WrapTransport, not Transport: client-go's transport.New rejects a
		// custom Transport whenever the config also carries real TLS/cert
		// data (HasCA(), HasCertAuth(), etc.) — which every real kubeconfig
		// does. WrapTransport composes with TLS instead of conflicting with
		// it, and by ignoring the transport it's handed and returning our
		// own, the real one is never even constructed, let alone dialed.
		config.WrapTransport = func(http.RoundTripper) http.RoundTripper {
			return refusingTransport{}
		}
		// rest.Config.TransportConfig() wires ExecProvider/AuthProvider into
		// the transport chain via Config.Wrap(...), which composes OUTSIDE
		// (i.e. runs before) WrapTransport above. Left alone, an exec- or
		// auth-provider kubeconfig (EKS/GKE/AKS, kubelogin, ...) would have
		// its credential-fetching RoundTripper invoked — spawning a process
		// or hitting a cloud IdP, possibly blocking on an interactive login
		// prompt — before a request ever reaches refusingTransport. Clearing
		// these fields ensures TransportConfig() never wraps in that
		// machinery at all; see rest.Config.TransportConfig in
		// k8s.io/client-go/rest/transport.go for the fields it reads to
		// decide whether to do so.
		config.ExecProvider = nil
		config.AuthProvider = nil
		config.AuthConfigPersister = nil
		config.BearerToken = ""
		config.BearerTokenFile = ""
		config.Username = ""
		config.Password = ""
		return config
	}
	return flags.ToDiscoveryClient()
}

// buildLookup turns discovered API resources into the two lookup tables
// Source.Resolve serves from. err is ServerPreferredResources' own return
// value: a *discovery.ErrGroupDiscoveryFailed means some groups didn't
// resolve (their cache entry was missing or stale, most likely) but others
// did — that's still usable data, not a hard failure. Any other error is.
func buildLookup(lists []*metav1.APIResourceList, err error) (
	shorthands map[string]kinds.Kind, plurals map[kinds.Kind]string,
	namespaced map[kinds.Kind]bool, ok bool,
) {
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, nil, nil, false
	}

	shorthands = map[string]kinds.Kind{}
	plurals = map[kinds.Kind]string{}
	namespaced = map[kinds.Kind]bool{}
	for _, list := range lists {
		if list == nil {
			continue
		}
		for _, resource := range list.APIResources {
			// A subresource's Name contains "/" (e.g. "deployments/scale")
			// and is never a spelling anyone types as a bare resource type.
			if strings.Contains(resource.Name, "/") || resource.Kind == "" {
				continue
			}
			kind := kinds.Kind(resource.Kind)
			plurals[kind] = resource.Name
			// The scope kubectl already recorded for every resource, which is
			// what tells a Node listing it has no namespace to be captioned
			// with. Free here: the cache entry carries it either way.
			namespaced[kind] = resource.Namespaced
			shorthands[strings.ToLower(resource.Name)] = kind
			shorthands[strings.ToLower(resource.Kind)] = kind
			if resource.SingularName != "" {
				shorthands[strings.ToLower(resource.SingularName)] = kind
			}
			for _, short := range resource.ShortNames {
				shorthands[strings.ToLower(short)] = kind
			}
		}
	}
	return shorthands, plurals, namespaced, true
}

// Source implements kinds.ShorthandSource by reading kubectl's on-disk
// discovery cache. The lookup is built once per process, on first use — a
// kx invocation that never types an unrecognized spelling pays no cost from
// this package at all.
type Source struct {
	once       sync.Once
	shorthands map[string]kinds.Kind
	plurals    map[kinds.Kind]string
	namespaced map[kinds.Kind]bool
}

// NewSource returns a Source ready to install via kinds.SetShorthandSource.
func NewSource() *Source {
	return &Source{}
}

// Resolve implements kinds.ShorthandSource.
func (s *Source) Resolve(spelling string) (kinds.Kind, string, bool) {
	s.once.Do(s.load)
	kind, ok := s.shorthands[strings.ToLower(spelling)]
	if !ok {
		return "", "", false
	}
	return kind, s.plurals[kind], true
}

// Namespaced implements kinds.ShorthandSource.
//
// A kind absent from the cache is reported unknown rather than assumed either
// way: the lookup is built from whatever groups resolved, and a group that
// did not is a gap in kx's knowledge, not evidence about scope.
func (s *Source) Namespaced(kind kinds.Kind) (bool, bool) {
	s.once.Do(s.load)
	scoped, ok := s.namespaced[kind]
	return scoped, ok
}

// withUnhandledErrorsSuppressed runs fn with apimachinery's
// utilruntime.ErrorHandlers replaced with a no-op, then restores whatever
// was installed before.
//
// k8s.io/client-go/discovery/cached/memory (the in-memory caching layer
// ConfigFlags.ToDiscoveryClient() wraps around the real discovery client)
// reports refusingTransport's error via utilruntime.HandleError, which by
// default logs to klog -> stderr. That would otherwise fire on every
// unrecognized token typed at kx — refusingTransport working as intended is
// not something the user needs to see printed as an "Unhandled Error".
//
// utilruntime.ErrorHandlers is a package-level var, not scoped to a caller
// or goroutine, so this only *looks* scoped to Source.load: nothing stops a
// concurrent goroutine elsewhere in the process from calling
// utilruntime.HandleError while this swap is in effect and having its error
// silently dropped, or from racing the assignment outright. kx is a
// single-threaded CLI — one command runs to completion before the next
// package's client-go code (e.g. internal/k8s) ever touches
// ErrorHandlers — so in practice this window is exactly the one synchronous
// discovery call below and nothing else observes the swap. It would not be
// safe to do this if kx ever ran concurrent client-go operations.
func withUnhandledErrorsSuppressed(fn func()) {
	previous := utilruntime.ErrorHandlers
	utilruntime.ErrorHandlers = nil
	defer func() { utilruntime.ErrorHandlers = previous }()
	fn()
}

// load populates the lookup tables. Any failure (no kubeconfig, no cache
// directory, a hard discovery error) leaves both maps empty — Resolve then
// simply never matches anything, identical to no source being installed.
func (s *Source) load() {
	s.shorthands = map[string]kinds.Kind{}
	s.plurals = map[kinds.Kind]string{}
	s.namespaced = map[kinds.Kind]bool{}

	client, err := newDiscoveryClient()
	if err != nil {
		return
	}
	var lists []*metav1.APIResourceList
	var discoveryErr error
	withUnhandledErrorsSuppressed(func() {
		lists, discoveryErr = client.ServerPreferredResources()
	})
	shorthands, plurals, namespaced, ok := buildLookup(lists, discoveryErr)
	if !ok {
		return
	}
	s.shorthands = shorthands
	s.plurals = plurals
	s.namespaced = namespaced
}
