// Package kinds maps kubectl shorthand (pods, deploy, svc) onto the canonical
// Kubernetes kind names used for state entries and event comparisons.
package kinds

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is a canonical Kubernetes kind name. An unrecognized resource type is
// carried through verbatim as a Kind, matching the Python `Kind | str` union —
// kx wraps kubectl, so it must not reject resource types it doesn't know.
type Kind string

const (
	Pod                     Kind = "Pod"
	Deployment              Kind = "Deployment"
	ReplicaSet              Kind = "ReplicaSet"
	StatefulSet             Kind = "StatefulSet"
	DaemonSet               Kind = "DaemonSet"
	CronJob                 Kind = "CronJob"
	Service                 Kind = "Service"
	HorizontalPodAutoscaler Kind = "HorizontalPodAutoscaler"
	Ingress                 Kind = "Ingress"
	ConfigMap               Kind = "ConfigMap"
	Secret                  Kind = "Secret"
	Job                     Kind = "Job"
	PersistentVolumeClaim   Kind = "PersistentVolumeClaim"
	Node                    Kind = "Node"
	Namespace               Kind = "Namespace"
	// Context is the pseudo-kind used for kubeconfig contexts. Not a
	// Kubernetes kind and never returned by Normalize — kubectl cannot `get`
	// one — but it is stored in state so the context command can tell a context
	// index from a resource index, which puts it on the same footing as the
	// real kinds for everything that reads state.
	Context Kind = "Context"
)

var kindMap = map[string]Kind{
	"po":                       Pod,
	"pod":                      Pod,
	"pods":                     Pod,
	"deployment":               Deployment,
	"deployments":              Deployment,
	"deploy":                   Deployment,
	"replicaset":               ReplicaSet,
	"replicasets":              ReplicaSet,
	"rs":                       ReplicaSet,
	"statefulset":              StatefulSet,
	"statefulsets":             StatefulSet,
	"sts":                      StatefulSet,
	"daemonset":                DaemonSet,
	"daemonsets":               DaemonSet,
	"ds":                       DaemonSet,
	"hpa":                      HorizontalPodAutoscaler,
	"horizontalpodautoscaler":  HorizontalPodAutoscaler,
	"horizontalpodautoscalers": HorizontalPodAutoscaler,
	"service":                  Service,
	"services":                 Service,
	"svc":                      Service,
	"ingress":                  Ingress,
	"ingresses":                Ingress,
	"configmap":                ConfigMap,
	"configmaps":               ConfigMap,
	"cm":                       ConfigMap,
	"secret":                   Secret,
	"secrets":                  Secret,
	"job":                      Job,
	"jobs":                     Job,
	"cronjob":                  CronJob,
	"cronjobs":                 CronJob,
	"pvc":                      PersistentVolumeClaim,
	"persistentvolumeclaim":    PersistentVolumeClaim,
	"persistentvolumeclaims":   PersistentVolumeClaim,
	"node":                     Node,
	"nodes":                    Node,
	"ns":                       Namespace,
	"namespace":                Namespace,
	"namespaces":               Namespace,
}

var pluralDisplay = map[Kind]string{
	Pod:                     "Pods",
	Deployment:              "Deployments",
	ReplicaSet:              "ReplicaSets",
	StatefulSet:             "StatefulSets",
	DaemonSet:               "DaemonSets",
	CronJob:                 "CronJobs",
	Service:                 "Services",
	HorizontalPodAutoscaler: "HorizontalPodAutoscalers",
	Ingress:                 "Ingresses",
	ConfigMap:               "ConfigMaps",
	Secret:                  "Secrets",
	Job:                     "Jobs",
	PersistentVolumeClaim:   "PersistentVolumeClaims",
	Node:                    "Nodes",
	Namespace:               "Namespaces",
	Context:                 "Contexts",
}

// ShorthandSource resolves a spelling kindMap does not recognize, such as a
// CRD's shortName. Injected so this package stays free of a discovery/
// client-go dependency — see internal/discovery.Source, the production
// implementation. A nil source (the default) means no fallback: identical
// to today's behavior.
type ShorthandSource interface {
	Resolve(spelling string) (kind Kind, plural string, ok bool)
}

var shorthandSource ShorthandSource

// SetShorthandSource installs the fallback consulted when kindMap misses.
// Called once, from cmd/kx/main.go, before any command runs — deliberately
// not from cli.NewRoot, so internal/cli's own tests (which call NewRoot
// directly, many times) never install a real discovery source that would
// read the ambient kubeconfig. Passing nil removes any previously installed
// source.
func SetShorthandSource(source ShorthandSource) {
	shorthandSource = source
}

// Spelling is one recognized resource spelling and the kind it maps to.
type Spelling struct {
	Name string
	Kind Kind
}

// Spellings returns every spelling kindMap recognizes, sorted by name.
//
// Exported for shell completion, which needs the spellings themselves rather
// than the yes/no answer IsKindSpelling gives. CRD short names are not
// included: resolving those means asking the discovery cache, and a completion
// that reads from disk on every Tab is one people switch off.
func Spellings() []Spelling {
	spellings := make([]Spelling, 0, len(kindMap))
	for name, kind := range kindMap {
		spellings = append(spellings, Spelling{Name: name, Kind: kind})
	}
	sort.Slice(spellings, func(i, j int) bool { return spellings[i].Name < spellings[j].Name })
	return spellings
}

// IsKindSpelling reports whether token names a known resource type.
func IsKindSpelling(token string) bool {
	if _, ok := kindMap[strings.ToLower(token)]; ok {
		return true
	}
	if shorthandSource != nil {
		if _, _, ok := shorthandSource.Resolve(token); ok {
			return true
		}
	}
	return false
}

// Normalize maps a kubectl resource type onto its canonical kind, passing
// unknown types through unchanged.
func Normalize(resourceType string) Kind {
	if kind, ok := kindMap[strings.ToLower(resourceType)]; ok {
		return kind
	}
	if shorthandSource != nil {
		if kind, _, ok := shorthandSource.Resolve(resourceType); ok {
			return kind
		}
	}
	return Kind(resourceType)
}

// displayPlural builds a caption plural from a kind and the API's own plural
// for it.
//
// Discovery reports the plural as the API resource *name* — "serviceaccounts",
// "networkpolicies" — which is lowercase by definition, since it is a URL path
// segment rather than a display string. Captioning with it directly is what put
// "serviceaccounts · diagnostics · 1 item" on screen beside "Pods" and
// "ConfigMaps".
//
// The kind is already the PascalCase spelling, so only the pluralising suffix
// is taken from the API — which is the part worth taking, since the API server
// knows the irregulars and a rule here would have to guess them.
func displayPlural(kind Kind, apiPlural string) string {
	lower := strings.ToLower(string(kind))
	switch {
	// "serviceaccount" -> "serviceaccounts" keeps the kind and takes "s";
	// "ingress" -> "ingresses" takes "es"; "endpoints" -> "endpoints" takes
	// nothing, which is right — the kind is already plural.
	case strings.HasPrefix(apiPlural, lower):
		return string(kind) + apiPlural[len(lower):]
	// "networkpolicy" -> "networkpolicies" is not a suffix at all: the stem
	// changes. Recognised rather than guessed, so it only applies when the API
	// actually spelled it that way.
	case strings.HasSuffix(lower, "y") && apiPlural == strings.TrimSuffix(lower, "y")+"ies":
		return strings.TrimSuffix(string(kind), "y") + "ies"
	}
	// An irregular nothing here models. The kind is still the right register to
	// caption in, which a lowercase URL segment never is.
	return string(kind) + "s"
}

// PluralDisplay renders a resource type for captions ("pods" -> "Pods"),
// passing unknown types through unchanged.
func PluralDisplay(resourceType string) string {
	if kind, ok := kindMap[strings.ToLower(resourceType)]; ok {
		if plural, ok := pluralDisplay[kind]; ok {
			return plural
		}
		return string(kind) + "s"
	}
	if shorthandSource != nil {
		if kind, plural, ok := shorthandSource.Resolve(resourceType); ok && kind != "" {
			return displayPlural(kind, plural)
		}
	}
	// A pseudo-kind has no kubectl spelling, so it never appears in kindMap.
	// It is still named by its canonical kind in captions and errors.
	if plural, ok := pluralDisplay[Kind(resourceType)]; ok {
		return plural
	}
	return resourceType
}

// PreviousLister reports whether the history entry one step back lists kind,
// so EnsureKind can offer the `kx back` hint. Implemented by the state service;
// declared here to keep kinds free of a state import (the Python version defers
// the import to break the same cycle).
type PreviousLister interface {
	PreviousLists(kind Kind) bool
}

// EnsureKind rejects an index that resolved to something other than expected.
//
// Every command that resolves an index against a kind reports the mismatch in
// this one shape, and the relist hint always names the canonical kind rather
// than whatever shorthand was typed — `kx get deployment`, never `kx get deploy`.
//
// Given a state service, an entry one step back that does list expected adds
// the `kx back` clause: relisting re-runs kubectl, while the listing the index
// came from is often still sitting in history.
func EnsureKind(index int, name string, kind, expected Kind, state PreviousLister) error {
	if kind == expected {
		return nil
	}
	back := ""
	if state != nil && state.PreviousLists(expected) {
		back = fmt.Sprintf(", or 'kx back' for the previous %s listing", expected)
	}
	return fmt.Errorf(
		"Index %d is %s/%s, not %s — run 'kx get %s' to relist%s.",
		index, kind, name, expected, strings.ToLower(string(expected)), back,
	)
}
