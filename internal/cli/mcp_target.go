package cli

import (
	"errors"
	"fmt"
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
	kind := kinds.Normalize(target.Kind)
	namespace := target.Namespace
	// Unknown scope (a CRD with no discovery cache) is treated as namespaced,
	// the same default every kx command takes.
	if namespaced, known := kinds.Namespaced(kind); known && !namespaced {
		if namespace != "" {
			return resolvedTarget{}, fmt.Errorf(
				"%s is cluster-scoped and takes no namespace — drop 'namespace'.", kind)
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
