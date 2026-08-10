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

	"github.com/jzills/kx/internal/kinds"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	shorthands map[string]kinds.Kind, plurals map[kinds.Kind]string, ok bool,
) {
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, nil, false
	}

	shorthands = map[string]kinds.Kind{}
	plurals = map[kinds.Kind]string{}
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
	return shorthands, plurals, true
}
