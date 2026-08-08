package cli

import (
	"strconv"
	"strings"

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

// allNamespacesNote explains why an -A listing has no indexes, since the
// absence is otherwise indistinguishable from a bug. It lives in render so the
// triage sweep can print the same sentence for the same reason.
const allNamespacesNote = render.AllNamespacesNote

// runGet is the shared body of `get` and `secret`.
//
// Numeric arguments are indexes into the current listing rather than names:
// `kx get pods 1 3` re-fetches those two pods. They are resolved to names,
// checked against the requested kind, and scoped to the namespace they were
// listed in.
func runGet(services Services, resource string, args []string, options getOptions) error {
	var indexes []int
	var extra []string
	for _, arg := range args {
		if index, err := strconv.Atoi(arg); err == nil {
			indexes = append(indexes, index)
			continue
		}
		extra = append(extra, arg)
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
		names := make([]string, 0, len(indexes))
		namespace := ""
		for _, index := range indexes {
			// FieldsExpecting rather than Fields: the resource type was named on
			// the command line, so an out-of-range index or an empty history can
			// be reported against that kind instead of against whatever listing
			// happens to be current.
			name, ns, err := services.State.FieldsExpecting(index, expected)
			if err != nil {
				return err
			}
			names = append(names, name)
			namespace = ns
		}
		// The listing's own namespace, unless the user named one.
		if namespace != "" && extractNamespace(extra) == "" {
			extra = append(extra, "-n", namespace)
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

	note := ""
	if allNamespaces(extra) {
		namespace = "all namespaces"
		note = allNamespacesNote
	}
	render.IndexedTable(output, resource, namespace, note)
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
