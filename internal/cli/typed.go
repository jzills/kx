package cli

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jzills/kx/internal/k8s"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
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
// extractString's reading, so a flag is read one way wherever kx reads it.
func flagValue(args []string, long, short string) string {
	value, _, _ := extractString(args, long, short)
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
	services.targets.record(services.Kubectl, resolved)
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

// parseTargetArg is parseRef for a command with -n and --context flags of its
// own (kx diag, kx tree, kx scan) that also takes kind/name. scope is those
// flags as argv (targetArgv): the commands read -n for a sweep, so it reaches
// here as a value rather than as kubectl argv. A cluster flag beside an index
// is refused, as it is everywhere.
func parseTargetArg(services Services, arg string, scope []string) (state.Ref, error) {
	if !typedShaped(arg) && clusterFromArgs(scope).IsZero() {
		return parseRef("index", arg)
	}
	target, err := resolveTargetArg(services, "index", arg, scope)
	if err != nil {
		return state.Ref{}, err
	}
	return target.Ref, nil
}

// targetArgv spells a command's own -n and --context values as the argv
// settleTyped reads, leaving out whichever is empty.
func targetArgv(namespace, context string) []string {
	var argv []string
	if namespace != "" {
		argv = append(argv, "--namespace="+namespace)
	}
	if context != "" {
		argv = append(argv, "--context="+context)
	}
	return argv
}

// typedContextUsage is the --context of a command that reads it only for a
// kind/name target.
const typedContextUsage = "Kubeconfig context of a kind/name target; defaults to the current one. An index carries its own"

// contextScope is a command's own --context as argv, for a command that
// takes no kubectl flags.
func contextScope(cmd *cobra.Command) []string {
	if !cmd.Flags().Changed("context") {
		return nil
	}
	context, _ := cmd.Flags().GetString("context")
	return []string{"--context=" + context}
}

// refuseContextFlag checks a command's own --context against its target
// before any client is built: it needs kind/name, a sweep refuses it
// (errContextWithoutTarget), and an index refuses it as it refuses every
// cluster flag.
func refuseContextFlag(given bool, args []string) error {
	switch {
	case !given:
		return nil
	case len(args) == 0:
		return errContextWithoutTarget
	case !typedShaped(args[0]):
		return clusterFlagBesideIndexError("--context")
	}
	return nil
}

// errContextWithoutTarget refuses --context on a sweep. A sweep's rows are
// numbered, and an index is spent in the cluster it was counted in; kx
// cannot number another cluster's rows into this one's history.
var errContextWithoutTarget = errors.New(
	"'--context' needs a kind/name target — a sweep's rows are numbered, and an " +
		"index is spent in the cluster it was counted in. To sweep another cluster, " +
		"switch to it with 'kx context'.")

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
// settled in and the context a flag chose, so the line reruns against the
// same resource from any namespace.
func typedInvocation(arg string, ref state.Ref, context string) string {
	if !ref.Typed() {
		return arg
	}
	if ref.Namespace != "" {
		arg += " -n " + ref.Namespace
	}
	if context != "" {
		arg += " --context " + context
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

// typedExists confirms a typed target names a resource the cluster has,
// reporting any other failure to ask as itself. kube is the command's own
// kubectl, which a flag beside the target has already sent to the cluster it
// chose (Services.forCluster).
func typedExists(kube kubectl.Service, target Resolved) error {
	argv := []string{"get", string(target.Kind), target.Name}
	if target.Namespace != "" {
		argv = append(argv, "-n", target.Namespace)
	}
	// Run rather than Probe: an exit code alone can't tell a missing
	// resource from a missing kubectl, an expired login or a bad context,
	// and reporting any of those as "not found" sends the user looking for
	// a typo that isn't there.
	_, err := kube.Run(argv)
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return StaleResourceError{
			Kind: target.Kind, Name: target.Name, Namespace: target.Namespace, Ref: target.Ref,
		}
	}
	return err
}

// targetLog is what the running command resolved its targets to, and the
// kubectl it resolved them with, so withRefresh can tell a failure about a
// kind/name target from one about an index. Only an index came from a
// listing; refreshing the listing for a typo in kind/name answers a mistake
// the user made with a table about something else.
//
// A command resolves and fails on one goroutine, and withRefresh reads the
// log only after the command returns, so it takes no lock.
type targetLog struct {
	kubectl kubectl.Service
	targets []Resolved
}

func (l *targetLog) record(kube kubectl.Service, resolved []Resolved) {
	if l == nil {
		return
	}
	l.kubectl = kube
	l.targets = append(l.targets, resolved...)
}

func (l *targetLog) reset() {
	if l == nil {
		return
	}
	l.kubectl, l.targets = nil, nil
}

// recorded is what the command resolved, and the kubectl it resolved it
// with.
func (l *targetLog) recorded() ([]Resolved, kubectl.Service) {
	if l == nil {
		return nil, nil
	}
	return l.targets, l.kubectl
}

// notFoundPattern is one resource in kubectl's not-found reply, as in
// `deployments.apps "api" not found`: the resource it asked for, and the
// name.
var notFoundPattern = regexp.MustCompile(`([A-Za-z0-9.-]+) "([^"]*)" not found`)

// namedMissing reports whether kubectl's reply says target is what it could
// not find. Both the name and the kind are compared: `kx delete deploy/api
// svc/api` names api twice, and a Service missing must not be read as the
// Deployment the command just deleted. A kind kx does not name itself, a
// CRD, may be spelled differently in the reply than it was typed, and is
// compared by name alone.
func namedMissing(stderr string, target Resolved) bool {
	for _, match := range notFoundPattern.FindAllStringSubmatch(stderr, -1) {
		if match[2] != target.Name {
			continue
		}
		replied, typed := kinds.Normalize(match[1]), kinds.Normalize(string(target.Kind))
		if replied == typed {
			return true
		}
		if _, known := kinds.Namespaced(replied); !known {
			if _, known := kinds.Namespaced(typed); !known {
				return true
			}
		}
	}
	return false
}

// typedNotFound is the error a not-found about kind/name targets is
// reported with, and whether it is about them: kubectl's reply names only
// kind/name targets. Asking for the one named confirms it is missing, and it
// is reported as that; otherwise kubectl's own error stands. Only a target
// kubectl named is asked for — another may be missing because the command
// itself just deleted it. When kubectl also names a target counted from the
// listing, the listing is what is stale, and the refresh reports it.
func typedNotFound(err error, targets []Resolved, kube kubectl.Service) (error, bool) {
	var reported kubectl.Error
	if !IsNotFound(err) || !errors.As(err, &reported) {
		return nil, false
	}
	var named []Resolved
	for _, target := range targets {
		if !namedMissing(reported.Stderr, target) {
			continue
		}
		if !target.Ref.Typed() {
			return nil, false
		}
		named = append(named, target)
	}
	if len(named) == 0 {
		return nil, false
	}
	for _, target := range named {
		var missing StaleResourceError
		if errors.As(typedExists(kube, target), &missing) {
			return missing, true
		}
	}
	return err, true
}

// typedArgDoc is the index argument's help on a command that also takes
// kind/name.
const typedArgDoc = "Row number from the current listing, @name for a mark, or kind/name such as deploy/api"

// acceptsTyped documents cmd's index argument as taking kind/name too. The
// Annotations map is copied rather than written into: it may be one of the
// shared ones (documentAnnotations).
func acceptsTyped(cmd *cobra.Command) *cobra.Command {
	merged := map[string]string{}
	for key, value := range cmd.Annotations {
		merged[key] = value
	}
	merged["arg.index"] = typedArgDoc
	merged["arg.indexes"] = typedArgDoc
	cmd.Annotations = merged
	return cmd
}

// parseTargetRef is parseRef for a single-reference command that also takes
// kind/name. The typed ref's namespace is not settled yet: settleRef does
// that once the command has split out the argv it reads -n from.
func parseTargetRef(name, arg string) (state.Ref, error) {
	if typedShaped(arg) {
		return parseTyped(arg)
	}
	return parseRef(name, arg)
}

// settleRef settles a ref parsed by parseTargetRef and applies the
// scope-flag rule to it, as resolveTargets does for several: a typed ref
// takes its namespace and cluster from extra, and an index or a mark refuses
// them.
func settleRef(services Services, ref state.Ref, extra []string) (state.Ref, error) {
	refs, err := settleTyped(services, []state.Ref{ref}, extra)
	if err != nil {
		return state.Ref{}, err
	}
	resolved, err := resolveParsed(refs, services.State.Resolve)
	if err != nil {
		return state.Ref{}, err
	}
	if err := refuseScopeFlagResolved(resolved, extra); err != nil {
		return state.Ref{}, err
	}
	services.targets.record(services.Kubectl, resolved)
	return refs[0], nil
}

// clusterKubectl sends every kubectl call a command makes to the cluster the
// command's flags chose. The argv a command forwards already carries them,
// but the reads it makes for itself — a drain's node preflight, a label's
// current-value fetch, exec's shell probe — were built without them, and
// beside kind/name, where those flags are allowed, would ask a different
// cluster than the one being acted on.
type clusterKubectl struct {
	kubectl.Service
	flags []string
}

func (k clusterKubectl) with(args []string) []string {
	at := len(args)
	for i, arg := range args {
		if arg == "--" {
			at = i
			break
		}
	}
	var out []string
	out = append(out, args[:at]...)
	// Only what the argv lacks: a command forwards the flags it was given,
	// and adding them again sent kubectl --context=prod twice.
	for _, flag := range k.flags {
		if !slices.Contains(args[:at], flag) {
			out = append(out, flag)
		}
	}
	return append(out, args[at:]...)
}

func (k clusterKubectl) Run(args []string) (string, error) { return k.Service.Run(k.with(args)) }
func (k clusterKubectl) RunInteractive(args []string, quiet bool) (int, error) {
	return k.Service.RunInteractive(k.with(args), quiet)
}
func (k clusterKubectl) Watch(args []string, onLine func(string) error) error {
	return k.Service.Watch(k.with(args), onLine)
}
func (k clusterKubectl) Probe(args []string) int { return k.Service.Probe(k.with(args)) }

// CurrentNamespace is the default namespace of the cluster the flags chose,
// as kubectl would use it.
func (k clusterKubectl) CurrentNamespace() string {
	return k8s.NamespaceFor(clusterFromArgs(k.flags))
}

// forCluster is s with its kubectl and API client sent to the cluster extra's
// flags choose, and s unchanged when they choose none. Called before a
// command builds anything from s: beside an index those flags are refused
// before any call is made, so this only ever acts for kind/name.
func (s Services) forCluster(extra []string) Services {
	cluster := clusterFromArgs(extra)
	if cluster.IsZero() {
		return s
	}
	s.Kubectl = clusterKubectl{Service: s.Kubectl, flags: clusterTokens(cluster)}
	s.Kubernetes = s.kubernetesFor(cluster)
	return s
}

// clusterTokens spells cluster as kubectl flags.
func clusterTokens(cluster k8s.Cluster) []string {
	var tokens []string
	for _, flag := range []struct{ name, value string }{
		{"--kubeconfig", cluster.Kubeconfig}, {"--context", cluster.Context},
		{"--cluster", cluster.Cluster}, {"--server", cluster.Server},
	} {
		if flag.value != "" {
			tokens = append(tokens, flag.name+"="+flag.value)
		}
	}
	return tokens
}

// chosenCluster names the cluster kube's flags chose, for a prompt asking
// before something is removed: " on context 'prod'". Empty when no flag chose
// one, where the cluster is the current context's, as it always was. Beside
// kind/name a flag sends the command elsewhere, and a prompt that named only
// the namespace asked "Delete Deployment/api in default?" of a delete bound
// for production.
func chosenCluster(kube kubectl.Service) string {
	flagged, ok := kube.(clusterKubectl)
	if !ok {
		return ""
	}
	cluster := clusterFromArgs(flagged.flags)
	for _, chosen := range []struct{ noun, value string }{
		{"context", cluster.Context}, {"cluster", cluster.Cluster},
		{"server", cluster.Server}, {"kubeconfig", cluster.Kubeconfig},
	} {
		if chosen.value != "" {
			return " on " + chosen.noun + " '" + chosen.value + "'"
		}
	}
	return ""
}

// CurrentContext is the context the flags chose, when they chose one by name.
func (k clusterKubectl) CurrentContext() string {
	if context := clusterFromArgs(k.flags).Context; context != "" {
		return context
	}
	return k.Service.CurrentContext()
}
