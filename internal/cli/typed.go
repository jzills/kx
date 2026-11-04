package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/spf13/cobra"
)

// A typed target names its resource the way kubectl does, kind/name, where an
// index or a mark would name it by a listing. It is what lets kx's own
// commands reach a resource whose name is already known — from an alert, a
// teammate, another tool's selection — without listing first and counting
// rows.
//
// It is held to exactly the checks an MCP target typed as kind and name gets
// (validateResolved): the flag-injection shapes, the 'all' refusal, every
// kubectl spelling of the kind normalized. Its namespace and cluster come from
// -n and --context as kubectl's would, defaulting to the kubeconfig's; that is
// the one place it differs from an index, which carries both from the listing
// it was counted in and so refuses those flags.

// typedShaped reports whether an argument is spelled as kind/name rather than
// as an index, a range or a mark. A flag never is, whatever its value holds,
// and neither is a range, which is what ".." means everywhere else.
func typedShaped(arg string) bool {
	return !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "@") &&
		!strings.Contains(arg, "..") && strings.Contains(arg, "/")
}

// parseTyped parses kind/name into a typed Ref with its kind normalized and
// its namespace not yet settled — that needs the command's flags, which
// settleTyped reads.
func parseTyped(arg string) (state.Ref, error) {
	spelling, name, _ := strings.Cut(arg, "/")
	if spelling == "" || name == "" || strings.Contains(name, "/") {
		return state.Ref{}, fmt.Errorf(
			"'%s' is not a resource — name one as kind/name, such as deploy/api.", arg)
	}
	kind, err := validateResolved(spelling, name, "")
	if err != nil {
		return state.Ref{}, err
	}
	if kind == kinds.Context {
		return state.Ref{}, errors.New(
			"A context is not a resource — switch to it with 'kx context'.")
	}
	return state.Ref{Kind: kind, Name: name}, nil
}

// clusterFromArgs reads the cluster kubectl's cluster-selecting flags choose,
// from argv as kx forwards it (--long=value) or as typed (--long value).
func clusterFromArgs(args []string) k8s.Cluster {
	return k8s.Cluster{
		Kubeconfig: flagValue(args, "--kubeconfig", ""),
		Context:    flagValue(args, "--context", ""),
		Cluster:    flagValue(args, "--cluster", ""),
		Server:     flagValue(args, "--server", "-s"),
	}
}

// flagValue is the last value argv gives a flag, "" when it is absent:
// kubectl takes the last of a repeated flag, so kx reads the same one.
func flagValue(args []string, long, short string) string {
	value := ""
	for i, arg := range args {
		for _, name := range []string{long, short} {
			if name == "" {
				continue
			}
			switch {
			case strings.HasPrefix(arg, name+"="):
				value = strings.TrimPrefix(arg, name+"=")
			case arg == name && i+1 < len(args):
				value = args[i+1]
			case len(name) == 2 && len(arg) > 2 && strings.HasPrefix(arg, name):
				value = strings.TrimPrefix(arg, name)
			}
		}
	}
	return value
}

// namespaceFor is the namespace kubectl would use for cluster when no -n is
// given: the current one, or the default namespace of the context a flag
// chose.
func (s Services) namespaceFor(cluster k8s.Cluster) string {
	if s.ClusterNamespace != nil {
		return s.ClusterNamespace(cluster)
	}
	if cluster.IsZero() {
		return s.Kubectl.CurrentNamespace()
	}
	return k8s.NamespaceFor(cluster)
}

// settleTyped gives each typed ref in refs the namespace it names a resource
// in: -n's when one is given, otherwise the kubeconfig's for the cluster the
// flags choose. A cluster-scoped kind keeps none, and refuses -n as kx get
// does. Index and mark refs are left as they are; whether a flag may stand
// beside them is refuseScopeFlagResolved's question.
func settleTyped(services Services, refs []state.Ref, extra []string) ([]state.Ref, error) {
	if !anyTyped(refs) {
		return refs, nil
	}
	if present, _ := extractBool(extra, "--all-namespaces", "-A"); present {
		return nil, allNamespacesBesideTypedError(firstSpelling(extra, "--all-namespaces", "-A"), "")
	}
	namespace := flagValue(extra, "--namespace", "-n")
	if namespace != "" {
		if err := validNamespace(namespace); err != nil {
			return nil, err
		}
	}
	fallback := ""
	settled := make([]state.Ref, len(refs))
	for i, ref := range refs {
		settled[i] = ref
		if !ref.Typed() {
			continue
		}
		if clusterScoped(string(ref.Kind)) {
			if namespace != "" {
				return nil, clusterScopedScopeError(
					firstSpelling(extra, "--namespace", "-n"), string(ref.Kind))
			}
			continue
		}
		if namespace != "" {
			settled[i].Namespace = namespace
			continue
		}
		if fallback == "" {
			fallback = services.namespaceFor(clusterFromArgs(extra))
		}
		settled[i].Namespace = fallback
	}
	return settled, nil
}

// allNamespacesBesideTypedError refuses -A beside kind/name, quoting the
// spelling given; hint is appended when the command can sweep instead.
func allNamespacesBesideTypedError(flag, hint string) error {
	message := fmt.Sprintf(
		"'%s' cannot be combined with kind/name — it names one resource, in one namespace.", flag)
	if hint != "" {
		message += " " + hint
	}
	return errors.New(message)
}

// sweepEverywhereHint is sweepInsteadHint for -A beside kind/name.
const sweepEverywhereHint = "Drop the flag, or drop the kind/name to sweep every namespace instead."

func anyTyped(refs []state.Ref) bool {
	for _, ref := range refs {
		if ref.Typed() {
			return true
		}
	}
	return false
}

func allTyped(resolved []Resolved) bool {
	for _, target := range resolved {
		if !target.Ref.Typed() {
			return false
		}
	}
	return len(resolved) > 0
}

// resolveTargets is resolveRefs for a command that also takes kind/name: it
// parses args, settles each typed target's namespace from extra, resolves
// every reference, and applies the scope-flag rule, all before the command
// acts on any.
func resolveTargets(services Services, name string, args, extra []string) ([]Resolved, error) {
	refs, err := parseRefsTyped(services.State, name, args, true)
	if err != nil {
		return nil, err
	}
	if refs, err = settleTyped(services, refs, extra); err != nil {
		return nil, err
	}
	resolved, err := resolveParsed(refs, services.State.Resolve)
	if err != nil {
		return nil, err
	}
	if err := refuseScopeFlagResolved(resolved, extra); err != nil {
		return nil, err
	}
	return resolved, nil
}

// resolveTargetArg is resolveTargets for a command that takes one reference.
func resolveTargetArg(services Services, name, arg string, extra []string) (Resolved, error) {
	resolved, err := resolveTargets(services, name, []string{arg}, extra)
	if err != nil {
		return Resolved{}, err
	}
	return resolved[0], nil
}

// parseTargetArg is parseRef for a client-go command (kx diag, kx tree, kx
// scan) that also takes kind/name, with namespace the value of its own -n
// flag: those commands read -n for a sweep, so it reaches here as a value
// rather than as kubectl argv.
func parseTargetArg(services Services, arg, namespace string) (state.Ref, error) {
	if !typedShaped(arg) {
		return parseRef("index", arg)
	}
	var extra []string
	if namespace != "" {
		extra = []string{"--namespace=" + namespace}
	}
	target, err := resolveTargetArg(services, "index", arg, extra)
	if err != nil {
		return state.Ref{}, err
	}
	return target.Ref, nil
}

// targetNoun names what a single-resource argument is, for a refusal that
// says what a flag cannot stand beside: "an index" covers a mark too, as it
// always has; kind/name is named as itself.
func targetNoun(arg string) string {
	if typedShaped(arg) {
		return "kind/name"
	}
	return "an index"
}

// targetWord is targetNoun without its article, for "drop the index".
func targetWord(arg string) string {
	if typedShaped(arg) {
		return "kind/name"
	}
	return "index"
}

// typedInvocation is the argument a report's invocation line shows for ref:
// the argument as typed, followed for a typed target by the namespace it was
// settled in, so the line reruns against the same resource from any
// namespace.
func typedInvocation(arg string, ref state.Ref) string {
	if ref.Typed() && ref.Namespace != "" {
		return arg + " -n " + ref.Namespace
	}
	return arg
}

// typedNamespaceUsage is the -n of a command that reads -n only for a
// kind/name target.
const typedNamespaceUsage = "Namespace of a kind/name target; defaults to the current namespace. An index carries its own"

// namespaceScope is a command's own -n as the argv settleTyped reads, for a
// command that takes no kubectl flags. Present when given, even empty, so
// the scope guard still sees it.
func namespaceScope(cmd *cobra.Command) []string {
	if !cmd.Flags().Changed("namespace") {
		return nil
	}
	namespace, _ := cmd.Flags().GetString("namespace")
	return []string{"--namespace=" + namespace}
}

// typedExists confirms a typed target names a resource the cluster has.
func typedExists(services Services, target Resolved) error {
	argv := []string{"get", string(target.Kind), target.Name}
	if target.Namespace != "" {
		argv = append(argv, "-n", target.Namespace)
	}
	if services.Kubectl.Probe(argv) == 0 {
		return nil
	}
	return StaleResourceError{
		Kind: target.Kind, Name: target.Name, Namespace: target.Namespace, Ref: target.Ref,
	}
}

// typedArgDoc is the index argument's help on a command that also takes
// kind/name.
const typedArgDoc = "Row number from the current listing, @name for a mark, or kind/name such as deploy/api"

// acceptsTyped documents cmd's index argument as taking kind/name too. The
// Annotations map is copied rather than written into: it may be one of the
// shared ones (documentAnnotations).
func acceptsTyped(cmd *cobra.Command) *cobra.Command {
	merged := make(map[string]string, len(cmd.Annotations)+2)
	for key, value := range cmd.Annotations {
		merged[key] = value
	}
	merged["arg.index"] = typedArgDoc
	merged["arg.indexes"] = typedArgDoc
	cmd.Annotations = merged
	return cmd
}
