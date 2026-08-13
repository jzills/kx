package cli

import (
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
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

// groupByNamespace collects resolved entries by namespace, preserving the order
// each namespace was first seen so the stitched table lists rows in roughly the
// order the indexes did. Always returns at least one group for a non-empty
// input, so callers can read groups[0] without a length check.
func groupByNamespace(entries []index.Entry) []namespaceGroup {
	var groups []namespaceGroup
	at := map[string]int{}
	for _, entry := range entries {
		position, seen := at[entry.Namespace]
		if !seen {
			at[entry.Namespace] = len(groups)
			groups = append(groups, namespaceGroup{Namespace: entry.Namespace})
			position = len(groups) - 1
		}
		groups[position].Names = append(groups[position].Names, entry.Name)
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
	indexArgs, extra := splitLeadingIndexes(args)
	var indexes []int
	if len(indexArgs) > 0 {
		var err error
		indexes, err = parseIndexes(services.State, "indexes", indexArgs)
		if err != nil {
			return err
		}
	}

	// Contexts live in kubeconfig, not on the server, so kubectl rejects
	// `get contexts`. Routing the spelling here keeps `kx get <thing>` the one
	// way to relist anything — including the hint a kind mismatch prints.
	switch strings.ToLower(resource) {
	case "context", "contexts":
		if len(indexes) == 0 {
			return listSwitchTargets(services, true)
		}
		return switchTo(services, "context", indexes[0], true)
	}

	if options.Decode || options.HasKey {
		return decodeSecrets(services, resource, indexes, extra, options)
	}

	if len(indexes) > 0 {
		expected := kinds.Normalize(resource)
		resolved := make([]index.Entry, 0, len(indexes))
		for _, idx := range indexes {
			// FieldsExpecting rather than Fields: the resource type was named on
			// the command line, so an out-of-range index or an empty history can
			// be reported against that kind instead of against whatever listing
			// happens to be current.
			name, ns, err := services.State.FieldsExpecting(idx, expected)
			if err != nil {
				return err
			}
			resolved = append(resolved, index.Entry{Name: name, Namespace: ns})
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
		if len(indexes) > 1 && isWatch(extra) {
			return fmt.Errorf(
				"--watch takes a single resource; %d indexes were given. "+
					"Watch one of them, or drop --watch to fetch them all.",
				len(indexes))
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
