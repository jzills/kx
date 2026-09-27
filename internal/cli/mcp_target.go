package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// mcpTarget names one resource in a tool call: kind and name (and a
// namespace, defaulting to the current one), a mark, or an index — a row
// number from the user's current kx listing, read live and resolved the same
// way `kx describe 3` would be. See mcp.go for what an index reads and does
// not do.
type mcpTarget struct {
	Kind      string `json:"kind,omitempty" jsonschema:"Resource type as kubectl spells it: pods, deploy, Deployment, a CRD's name. Required unless mark or index is given."`
	Name      string `json:"name,omitempty" jsonschema:"Resource name. Required unless mark or index is given."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace; defaults to the kubeconfig's current namespace. Omit for cluster-scoped kinds such as nodes."`
	Mark      string `json:"mark,omitempty" jsonschema:"A kx mark (with or without the leading @) in place of kind, name and namespace. list_marks shows them."`
	Index     int    `json:"index,omitempty" jsonschema:"A row number from the user's current kx listing, e.g. the 3 in 'diagnose 3'; confirm the resolved name back to the user before acting on it."`
}

// resolvedTarget is a target after normalisation. Mark is kept so a result can
// say which mark produced it, the way kx diag --json does.
type resolvedTarget struct {
	Kind      kinds.Kind
	Name      string
	Namespace string
	Mark      string
}

// targetAmbiguous refuses a target that names more than one of its three
// mutually exclusive shapes: kind/name(/namespace), a mark, or an index.
func targetAmbiguous(target mcpTarget) error {
	groups := 0
	if target.Mark != "" {
		groups++
	}
	if target.Index != 0 {
		groups++
	}
	if target.Kind != "" || target.Name != "" || target.Namespace != "" {
		groups++
	}
	if groups > 1 {
		return errors.New("A target is one of kind and name, a mark, or an index — give only one.")
	}
	return nil
}

func (d mcpDeps) resolveTarget(target mcpTarget) (resolvedTarget, error) {
	if err := targetAmbiguous(target); err != nil {
		return resolvedTarget{}, err
	}
	if target.Index != 0 {
		// The CLI's own resolution — index.Resolve's out-of-range refusal,
		// checkContext's mismatch refusal, ErrNoState — is reused wholesale,
		// so an agent's index is refused exactly as `kx describe 3` would be.
		name, namespace, kind, err := d.State.Resolve(state.Ref{Index: target.Index})
		if err != nil {
			return resolvedTarget{}, err
		}
		kind, err = validateResolved(string(kind), name, namespace)
		if err != nil {
			return resolvedTarget{}, err
		}
		return resolvedTarget{Kind: kind, Name: name, Namespace: namespace}, nil
	}
	if mark := strings.TrimPrefix(target.Mark, "@"); mark != "" {
		// state's own resolution, so a mark taken in another context is
		// refused here exactly as `kx logs @api` refuses it.
		name, namespace, kind, err := d.State.Resolve(state.Ref{Mark: mark})
		if err != nil {
			return resolvedTarget{}, err
		}
		kind, err = validateResolved(string(kind), name, namespace)
		if err != nil {
			return resolvedTarget{}, err
		}
		return resolvedTarget{Kind: kind, Name: name, Namespace: namespace, Mark: mark}, nil
	}
	if target.Kind == "" || target.Name == "" {
		return resolvedTarget{}, errors.New("A target needs a kind and a name, a mark, or an index.")
	}
	if strings.ContainsAny(target.Kind, ",/") {
		return resolvedTarget{}, fmt.Errorf(
			"'%s' is not one resource type — give the kind and the name separately, one resource per target.",
			target.Kind)
	}
	if err := validKind(target.Kind); err != nil {
		return resolvedTarget{}, err
	}
	spelling := coreGroupSpelling(target.Kind)
	if err := refuseAll(spelling); err != nil {
		return resolvedTarget{}, err
	}
	if err := validObjectName(target.Name); err != nil {
		return resolvedTarget{}, err
	}
	if target.Namespace != "" {
		if err := validNamespace(target.Namespace); err != nil {
			return resolvedTarget{}, err
		}
	}
	kind := kinds.Normalize(spelling)
	namespace := target.Namespace
	// Unknown scope (a CRD with no discovery cache) is treated as namespaced,
	// the same default every kx command takes.
	if clusterScoped(spelling) {
		if namespace != "" {
			return resolvedTarget{}, clusterScopedScopeError("namespace", spelling)
		}
	} else if namespace == "" {
		namespace = d.Kubectl.CurrentNamespace()
	}
	return resolvedTarget{Kind: kind, Name: target.Name, Namespace: namespace}, nil
}

// coreGroupDotted is a kind spelled with the core API group's empty group
// made explicit: "secrets." (resource and empty group) or "secrets.v1."
// (resource, version v1, empty group).
var coreGroupDotted = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)(\.v1)?\.$`)

// coreGroupSpelling reduces a core-group dotted spelling to its plain form,
// so "secrets.v1." normalises to Secret exactly as "secrets" does.
//
// kubectl reads both spellings as core/v1 resources, but kinds.Normalize
// passes them through verbatim — and every check keyed on the canonical kind
// (Secret redaction above all, and the cluster-scope table) would then miss
// them, and a mark would store the odd spelling. A real API group
// (certificates.cert-manager.io, deployments.v1.apps) is left untouched.
func coreGroupSpelling(kind string) string {
	if match := coreGroupDotted.FindStringSubmatch(kind); match != nil {
		return match[1]
	}
	return kind
}

// getArgs builds the kubectl arguments naming a resolved target, leaving -n
// off a cluster-scoped one.
func (r resolvedTarget) getArgs(extra ...string) []string {
	args := []string{"get", string(r.Kind), r.Name}
	if r.Namespace != "" {
		args = append(args, "-n", r.Namespace)
	}
	return append(args, extra...)
}

// Every kind, name and namespace a tool is given lands in kubectl's argv, and
// kubectl reads anything with a leading '-' as a flag: a name of "-lapp=api"
// turns an existence check into a selector query, and "--server=…" sends the
// kubeconfig's credentials elsewhere. So each is held to the shape Kubernetes
// itself gives it before it goes near a command line.
var (
	kindPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*$`)
	// A DNS subdomain — the shape Kubernetes requires of most object names —
	// plus ':' anywhere after the first character, because RBAC's own objects
	// are named system:aggregate-to-admin and system:controller:…. A colon is
	// inert in argv; only a leading '-' is not, and the first character must
	// still be a letter or digit.
	objectNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.:]*[a-z0-9:])?$`)
	// A DNS label, the stricter shape every namespace has: no '.', no ':'.
	namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

const (
	maxObjectNameLength = 253
	maxNamespaceLength  = 63
)

func validKind(kind string) error {
	if !kindPattern.MatchString(kind) {
		return fmt.Errorf(
			"kind '%s' is not a resource type — give one such as pods, deploy or a CRD's plural name.", kind)
	}
	return nil
}

func validObjectName(name string) error {
	if len(name) > maxObjectNameLength || !objectNamePattern.MatchString(name) {
		return fmt.Errorf(
			"name '%s' is not a Kubernetes name — use lowercase letters, digits, '-', '.' and ':', starting with a letter or digit.",
			name)
	}
	return nil
}

func validNamespace(namespace string) error {
	if len(namespace) > maxNamespaceLength || !namespacePattern.MatchString(namespace) {
		return fmt.Errorf(
			"namespace '%s' is not a Kubernetes namespace — use lowercase letters, digits and '-', starting and ending with a letter or digit.",
			namespace)
	}
	return nil
}

// refuseAll refuses kubectl's "all" category, which names many resource
// types rather than one.
func refuseAll(spelling string) error {
	if strings.EqualFold(spelling, "all") {
		return errors.New("'all' is not one resource type — give the resource's own kind.")
	}
	return nil
}

// validateResolved holds a target resolved via a mark or an index — whose
// kind, name and namespace came from the state file rather than from the
// caller directly — to exactly the checks a typed target gets as it is
// parsed: the flag-injection shapes, the 'all' refusal, and the core-group
// dotted spelling reduced before the kind is normalised. It returns the
// canonical kind, which is what reaches argv and every kind-keyed check
// (Secret redaction above all). Without it either path was trusted outright.
func validateResolved(kind, name, namespace string) (kinds.Kind, error) {
	if err := validKind(kind); err != nil {
		return "", err
	}
	spelling := coreGroupSpelling(kind)
	if err := refuseAll(spelling); err != nil {
		return "", err
	}
	if err := validObjectName(name); err != nil {
		return "", err
	}
	if namespace != "" {
		if err := validNamespace(namespace); err != nil {
			return "", err
		}
	}
	return kinds.Normalize(spelling), nil
}
