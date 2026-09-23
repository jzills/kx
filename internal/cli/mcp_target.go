package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
)

// mcpTarget names one resource in a tool call: kind and name (and a namespace,
// defaulting to the current one), or a mark. Never an index — see mcp.go.
type mcpTarget struct {
	Kind      string `json:"kind,omitempty" jsonschema:"Resource type as kubectl spells it: pods, deploy, Deployment, a CRD's name. Required unless mark is given."`
	Name      string `json:"name,omitempty" jsonschema:"Resource name. Required unless mark is given."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace; defaults to the kubeconfig's current namespace. Omit for cluster-scoped kinds such as nodes."`
	Mark      string `json:"mark,omitempty" jsonschema:"A kx mark (with or without the leading @) in place of kind, name and namespace. list_marks shows them."`
}

// resolvedTarget is a target after normalisation. Mark is kept so a result can
// say which mark produced it, the way kx diag --json does.
type resolvedTarget struct {
	Kind      kinds.Kind
	Name      string
	Namespace string
	Mark      string
}

func (d mcpDeps) resolveTarget(target mcpTarget) (resolvedTarget, error) {
	if mark := strings.TrimPrefix(target.Mark, "@"); mark != "" {
		if target.Kind != "" || target.Name != "" || target.Namespace != "" {
			return resolvedTarget{}, errors.New(
				"Give a target either a mark or kind and name, not both — a mark already records its kind, name and namespace.")
		}
		// state's own resolution, so a mark taken in another context is
		// refused here exactly as `kx logs @api` refuses it.
		name, namespace, kind, err := d.State.Resolve(state.Ref{Mark: mark})
		if err != nil {
			return resolvedTarget{}, err
		}
		return resolvedTarget{Kind: kind, Name: name, Namespace: namespace, Mark: mark}, nil
	}
	if target.Kind == "" || target.Name == "" {
		return resolvedTarget{}, errors.New("A target needs a kind and a name, or a mark.")
	}
	if strings.ContainsAny(target.Kind, ",/") {
		return resolvedTarget{}, fmt.Errorf(
			"'%s' is not one resource type — give the kind and the name separately, one resource per target.",
			target.Kind)
	}
	if err := validKind(target.Kind); err != nil {
		return resolvedTarget{}, err
	}
	if strings.EqualFold(target.Kind, "all") {
		return resolvedTarget{}, errors.New("'all' is not one resource type — give the resource's own kind.")
	}
	if err := validObjectName("name", target.Name); err != nil {
		return resolvedTarget{}, err
	}
	if target.Namespace != "" {
		if err := validObjectName("namespace", target.Namespace); err != nil {
			return resolvedTarget{}, err
		}
	}
	kind := kinds.Normalize(target.Kind)
	namespace := target.Namespace
	// Unknown scope (a CRD with no discovery cache) is treated as namespaced,
	// the same default every kx command takes.
	if clusterScoped(target.Kind) {
		if namespace != "" {
			return resolvedTarget{}, clusterScopedScopeError("namespace", target.Kind)
		}
	} else if namespace == "" {
		namespace = d.Kubectl.CurrentNamespace()
	}
	return resolvedTarget{Kind: kind, Name: target.Name, Namespace: namespace}, nil
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
	// A DNS subdomain, the shape Kubernetes requires of most object names and
	// (as a stricter DNS label) of every namespace.
	objectNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
)

const maxObjectNameLength = 253

func validKind(kind string) error {
	if !kindPattern.MatchString(kind) {
		return fmt.Errorf(
			"kind '%s' is not a resource type — give one such as pods, deploy or a CRD's plural name.", kind)
	}
	return nil
}

// validObjectName checks a name or namespace; field is which of the two, so the
// refusal names the argument to fix.
func validObjectName(field, value string) error {
	if len(value) > maxObjectNameLength || !objectNamePattern.MatchString(value) {
		return fmt.Errorf(
			"%s '%s' is not a Kubernetes name — use lowercase letters, digits, '-' and '.', starting and ending with a letter or digit.",
			field, value)
	}
	return nil
}
