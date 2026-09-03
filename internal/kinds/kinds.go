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
	// Namespaced reports whether a kind is namespaced, and whether the
	// source knows. "Don't know" is distinct from "cluster-scoped": a
	// discovery cache that is missing, stale or partial must leave the
	// caller on its existing behaviour rather than have kx decide a
	// namespaced resource has no namespace.
	Namespaced(kind Kind) (namespaced, known bool)
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

// clusterScoped are the kinds kx names itself that live outside any namespace.
//
// Only the kinds in the Kind constants above: the full set on a real cluster
// runs to thirty-odd and grows with every CRD, which is exactly why the
// discovery cache is the primary source. This table exists so the kinds kx
// hard-codes are still answered correctly with no cache at all — a fresh
// machine, or a kubeconfig kubectl has never populated one for.
//
// Context is not here. It is not a Kubernetes kind and never reaches a
// namespace question; kx already special-cases it everywhere it matters.
var clusterScoped = map[Kind]bool{
	Node:      true,
	Namespace: true,
}

// namespacedKinds are the kinds kx names itself that live inside one. Written
// out rather than derived as "everything not in clusterScoped", so a kind
// added to the constants above without a scope is reported unknown instead of
// silently assumed namespaced.
var namespacedKinds = map[Kind]bool{
	Pod: true, Deployment: true, ReplicaSet: true, StatefulSet: true,
	DaemonSet: true, CronJob: true, Service: true, HorizontalPodAutoscaler: true,
	Ingress: true, ConfigMap: true, Secret: true, Job: true,
	PersistentVolumeClaim: true,
}

// Namespaced reports whether a kind lives inside a namespace, and whether that
// is known at all.
//
// The static table is consulted first so a stale or partial discovery cache
// can never make kx treat a Pod as cluster-scoped. Anything kx does not name
// itself — every CRD — is answered by the installed source, which reads the
// scope kubectl already recorded per resource.
//
// The third answer, "not known", matters as much as the other two: it is what
// a caller gets for a CRD with no discovery cache, and it must leave existing
// behaviour alone rather than guess.
func Namespaced(kind Kind) (namespaced, known bool) {
	if namespacedKinds[kind] {
		return true, true
	}
	if clusterScoped[kind] {
		return false, true
	}
	if shorthandSource == nil {
		return false, false
	}
	return shorthandSource.Namespaced(kind)
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

// ListCommand names the command that relists a kind, in the plural spelling
// every other kx surface uses.
//
// `kx get pods`, not `kx get pod`. Both work — kubectl takes either — but the
// singular appeared nowhere else in kx, and the sentences this is spliced into
// already name the kind in the plural two words later ("to relist Pods").
//
// Built from PluralDisplay rather than by suffixing here: that already carries
// the irregulars ("Ingress" -> "Ingresses") and resolves a CRD's plural
// through discovery, so this is a lowering of a spelling kx already computes
// rather than a second rule that would have to learn the same exceptions.
func ListCommand(kind Kind) string {
	return "kx get " + strings.ToLower(PluralDisplay(string(kind)))
}

// EnsureKind rejects an index that resolved to something other than expected.
//
// Every command that resolves an index against a kind reports the mismatch in
// this one shape, and the relist hint always names the canonical kind rather
// than whatever shorthand was typed — `kx get deployments`, never `kx get deploy`.
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
		"Index %d is %s/%s, not %s — run '%s' to relist%s.",
		index, kind, name, expected, ListCommand(expected), back,
	)
}

// Set is an ordered set of kinds — the kinds one command works on, in the
// order its help and its errors name them.
//
// A slice rather than the map[Kind]bool these used to be. Membership was all
// the maps were for, but the same list also has to be *printed* now that an
// "unsupported kind" error says which kinds are supported, and map iteration
// is randomized: a map cannot name the same kinds in the same order twice, so
// the message would differ between runs and could not be pinned by a test.
type Set []Kind

// Has reports whether the set contains a kind. Linear, over a handful of
// entries — which is every caller.
func (s Set) Has(kind Kind) bool {
	for _, member := range s {
		if member == kind {
			return true
		}
	}
	return false
}

// List renders the set the way a sentence names it: "Pods", "Pods and
// Deployments", "Pods, Deployments and StatefulSets".
//
// Plurals come from PluralDisplay, so "Ingress" reads as "Ingresses" rather
// than the "Ingresss" a bare +"s" would produce.
func (s Set) List() string {
	names := make([]string, 0, len(s))
	for _, kind := range s {
		names = append(names, PluralDisplay(string(kind)))
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
