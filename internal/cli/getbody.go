package cli

import (
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// getOptions carries the flags `get` and `secret` share. They delegate to the
// same body so that shadowing the `secret` kind spelling costs none of the
// listing behaviour.
type getOptions struct {
	Match  string
	Decode bool
	Key    string
	HasKey bool
	Yes    bool
}

// namespaceGroup is a run of resolved names sharing a namespace, in the order
// their indexes were given.
type namespaceGroup struct {
	Namespace string
	Names     []string
}

// groupByNamespace collects resolved references by namespace, preserving the
// order each namespace was first seen so the stitched table lists rows in
// roughly the order the indexes did. Always returns at least one group for a
// non-empty input, so callers can read groups[0] without a length check.
//
// Reads the namespace off each Resolved rather than resolving it again — the
// resolution already happened once, in resolveRefs.
func groupByNamespace(resolved []Resolved) []namespaceGroup {
	var groups []namespaceGroup
	at := map[string]int{}
	for _, target := range resolved {
		position, seen := at[target.Namespace]
		if !seen {
			at[target.Namespace] = len(groups)
			groups = append(groups, namespaceGroup{Namespace: target.Namespace})
			position = len(groups) - 1
		}
		groups[position].Names = append(groups[position].Names, target.Name)
	}
	return groups
}

// runGet is the shared body of `get` and `secret`.
//
// Numeric arguments are indexes into the current listing rather than names:
// `kx get pods 1 3` re-fetches those two pods. They are resolved to names,
// checked against the requested kind, and scoped to the namespace they were
// listed in.
func runGet(services Services, resource string, args []string, options getOptions) error {
	// Indexes lead, kubectl's flags follow — the same split describe/logs use
	// (splitLeadingIndexes), rather than scanning every argument for
	// something index-shaped. A kubectl flag value can legitimately contain
	// ".." (JSONPath's recursive descent, e.g. -o jsonpath={..metadata.name}),
	// and a scan-anywhere loop that expanded it as a range broke that
	// passthrough outright instead of erroring or ignoring it.
	// Read before anything saves over it: a listing that finds nothing still
	// becomes the current one, and the note offering the way back has to name
	// what it displaced. Errors are ignored deliberately — no state yet is the
	// ordinary first-run case, and it means there is nothing to offer.
	previous, _ := services.State.Load()

	indexArgs, extra := splitLeadingIndexes(args)
	var refs []state.Ref
	if len(indexArgs) > 0 {
		var err error
		refs, err = parseRefs(services.State, "indexes", indexArgs)
		if err != nil {
			return err
		}
	}

	// Contexts live in kubeconfig, not on the server, so kubectl rejects
	// `get contexts`. Routing the spelling here keeps `kx get <thing>` the one
	// way to relist anything — including the hint a kind mismatch prints.
	switch strings.ToLower(resource) {
	case "context", "contexts":
		if len(refs) == 0 {
			return listSwitchTargets(services, true)
		}
		// A mark names a Kubernetes resource pinned by kx state, not a
		// kubeconfig context — there is nothing for it to resolve against
		// here, so it is refused rather than silently spent as index 0. See
		// markRefusedForSlot (refs.go): newSwitchCommand hits the same case
		// for `kx ns`/`kx context` and shares this wording.
		if refs[0].Mark != "" {
			return markRefusedForSlot(refs[0], "contexts")
		}
		return switchTo(services, "context", refs[0].Index, true)
	}

	// A namespace flag on a cluster-scoped kind is refused, not forwarded — the
	// same call kx already makes for a scope flag beside an index. Checked
	// after the contexts branch, which is not a Kubernetes kind and never
	// reaches a namespace question, and before anything runs: the point of
	// refusing is that nothing about the cluster is read on a contradiction.
	if flag := scopeFlagIn(extra); flag != "" && clusterScoped(resource) {
		return clusterScopedScopeError(flag, resource)
	}

	if options.Decode || options.HasKey {
		// Resolved only when the command already names a Secret-shaped
		// resource and carries indexes: otherwise decodeSecrets's own guards
		// (--decode required, kind mismatch) are what should fire, and firing
		// resolveRefsExpecting first would replace those messages with a
		// resolution error about an index that was never going to be
		// fetched. When it does apply, resolving the whole batch here —
		// before decodeSecrets fetches or renders anything — is what stops a
		// bad index late in the batch from letting an earlier one's secret
		// reach the terminal first.
		var resolved []Resolved
		if options.Decode && kinds.Normalize(resource) == kinds.Secret && len(indexArgs) > 0 {
			var err error
			resolved, err = resolveParsedExpecting(services.State, refs, kinds.Secret)
			if err != nil {
				return err
			}
		}
		return decodeSecrets(services, resource, resolved, extra, options)
	}

	if len(refs) > 0 {
		expected := kinds.Normalize(resource)
		// resolveRefsExpecting resolves every index before any of them is
		// acted on, so an out-of-range index late in the batch is caught
		// before the first kubectl call rather than after some of them have
		// already run. Expecting rather than resolveRefs's plain Resolve: the
		// resource type was named on the command line, so a failure — out of
		// range, no state, or an index left over from a listing of a
		// different kind — is reported against that kind, the way
		// FieldsExpecting always has, instead of generically or silently
		// fetched as whatever the index actually names.
		resolved, err := resolveParsedExpecting(services.State, refs, expected)
		if err != nil {
			return err
		}
		groups := groupByNamespace(resolved)

		// kubectl watches one named resource at a time — "you may only watch a
		// single resource or type of resource at a time" — and a watch is one
		// long-lived stream, so it cannot be split per namespace the way a
		// fetch can. Reported here because forwarding it produced an answer
		// about the wrong thing: the fallback below scopes every name to the
		// first group's namespace, so a selection spanning namespaces came
		// back as "pods ... not found", which reads as a resource that is
		// gone rather than a request kubectl will not serve.
		if len(resolved) > 1 && isWatch(extra) {
			return fmt.Errorf(
				"--watch takes a single resource; %d indexes were given. "+
					"Watch one of them, or drop --watch to fetch them all.",
				len(resolved))
		}

		// Indexes from an -A listing can land in different namespaces, and
		// kubectl cannot fetch named resources across namespaces in one call.
		// One call per namespace, stitched back together. An explicit -n means
		// the user overrode the scope, so there is nothing to span.
		if len(groups) > 1 && extractNamespace(extra) == "" {
			get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index}
			stop := render.Status("fetching " + resource)
			output, err := get.ExecuteGroups(resource, options.Match, groups, extra)
			stop()
			if err != nil {
				return err
			}
			render.IndexedTable(output, resource, render.AllNamespaces)
			if output.Empty() {
				render.PreviousListingNote(previous)
			}
			return nil
		}

		names := make([]string, 0, len(resolved))
		for _, entry := range resolved {
			names = append(names, entry.Name)
		}
		// The listing's own namespace, unless the user named one.
		if groups[0].Namespace != "" && extractNamespace(extra) == "" {
			extra = append(extra, "-n", groups[0].Namespace)
		}
		extra = append(names, extra...)
	}

	if isWatch(extra) {
		// A watch stream never completes, so there is no finished table to
		// index or save (Run() would otherwise block forever, since kubectl
		// get --watch never exits on its own). For the default/wide,
		// single-namespace table shape, kx tracks ADDED/MODIFIED/DELETED via
		// --output-watch-events and redraws a live themed table in place.
		// Anything else (-o json/yaml/name/custom-columns, -A) streams
		// straight through instead, the same way `logs -f` does — re-theming
		// non-tabular output doesn't make sense.
		if wantsLiveTable(extra) {
			return runWatch(services, resource, extra)
		}
		render.Caption("watches can't be indexed — streaming kubectl output directly")
		_, err := services.Kubectl.RunInteractive(append([]string{"get", resource}, extra...), false)
		return err
	}

	get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index}
	stop := render.Status("fetching " + resource)
	output, namespace, err := get.Execute(resource, options.Match, extra)
	stop()
	if err != nil {
		return err
	}

	if allNamespaces(extra) {
		namespace = render.AllNamespaces
	}
	render.IndexedTable(output, resource, namespace)
	if output.Empty() {
		render.PreviousListingNote(previous)
	}
	return nil
}

// switchTo activates an indexed namespace or context.
func switchTo(services Services, label string, index int, isContext bool) error {
	command := SwitchCommand{Kubectl: services.Kubectl, State: services.State}
	stop := render.Status("switching " + label)
	var name string
	var err error
	if isContext {
		name, err = command.context(index)
	} else {
		name, err = command.namespace(index)
	}
	stop()
	if err != nil {
		return err
	}
	render.Success("Switched to '" + name + "'")
	return nil
}
