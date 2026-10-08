package cli

import (
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
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

// resolveCovered resolves refs for a resource naming several kinds, refusing
// any that resolves to a kind the resource does not list (kinds.Covers) —
// the check every other kx get <kind> N makes through ResolveExpecting.
// Without it, kx get deploy,svc 3 after kx get pods fetched Pod 3 and saved
// it over the listing, and kx get all 3 --decode printed a Secret.
//
// Every ref is resolved before any is refused or fetched, as
// resolveRefsExpecting does, so the refusal names the first row it finds.
func resolveCovered(resolver IndexResolver, refs []state.Ref, resource string) ([]Resolved, error) {
	resolved, err := resolveParsed(refs, resolver.Resolve)
	if err != nil {
		return nil, err
	}
	for _, target := range resolved {
		if !kinds.Covers(resource, target.Kind) {
			return nil, notCoveredError(target, resource)
		}
	}
	return resolved, nil
}

// notCoveredError refuses a row a resource naming several kinds does not
// list, in the shape kinds.EnsureKind gives a row of the wrong kind. A mark
// is pointed at its own kind instead: it names one resource whatever is
// listed, so relisting changes nothing about it.
func notCoveredError(target Resolved, resource string) error {
	subject := fmt.Sprintf("%s/%s, which '%s' does not include", target.Kind, target.Name, resource)
	if target.Ref.Mark != "" {
		return fmt.Errorf("%s is %s — run '%s %s' to fetch it.",
			target.Ref, subject, kinds.ListCommand(target.Kind), target.Ref)
	}
	return fmt.Errorf("Index %s is %s — run '%s' to relist.",
		target.Ref, subject, kinds.ListCommand(kinds.Kind(resource)))
}

// soleKind is the one kind every resolved row is, or "" when they span kinds.
func soleKind(resolved []Resolved) kinds.Kind {
	for _, target := range resolved[1:] {
		if target.Kind != resolved[0].Kind {
			return ""
		}
	}
	return resolved[0].Kind
}

// fetchNames are the names a kubectl get of resolved is given: bare when the
// command names their one kind, Kind/name when they span kinds — the only
// spelling in which kubectl takes several kinds beside names.
func fetchNames(resolved []Resolved, spanning bool) []Resolved {
	if !spanning {
		return resolved
	}
	named := make([]Resolved, 0, len(resolved))
	for _, target := range resolved {
		target.Name = string(target.Kind) + "/" + target.Name
		named = append(named, target)
	}
	return named
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

	// Rows of a listing of several kinds are fetched again as the kind they
	// are — kx get all 2 is kx get deployment 2 — since kubectl takes no list
	// of kinds beside names. Checked against "all" as a kind, every index
	// was refused for not being one. Rows of several kinds are named
	// Kind/name instead (spanning), and stay a listing of the resource they
	// were asked with: kubectl is given none beside them (see getArgs), but
	// the caption, the saved entry and the command to relist it name it.
	// Either way each row must be one the resource lists (resolveCovered).
	//
	// Resolved once, here, and not again below: each resolve loads the state
	// file, and kx get all 1..200 loaded it four hundred times.
	var resolved []Resolved
	spanning := false
	if kinds.Several(resource) && len(refs) > 0 {
		var err error
		resolved, err = resolveCovered(services.State, refs, resource)
		if err != nil {
			return err
		}
		if kind := soleKind(resolved); kind == "" {
			spanning = true
		} else {
			resource = string(kind)
		}
	}

	// A namespace flag on a cluster-scoped kind is refused, not forwarded — the
	// same call kx already makes for a scope flag beside an index. Checked
	// after the contexts branch, which is not a Kubernetes kind and never
	// reaches a namespace question, and before anything runs: the point of
	// refusing is that nothing about the cluster is read on a contradiction.
	if flag := scopeFlagIn(extra); flag != "" && clusterScoped(string(listingKind(resource))) {
		return clusterScopedScopeError(flag, resource)
	}
	// An index carries its cluster as every index command's does, so a flag
	// choosing another one is refused here too — --decode included, which
	// would otherwise print a same-named Secret from the other cluster.
	if flag := clusterFlagIn(extra); flag != "" && len(refs) > 0 {
		return clusterFlagBesideIndexError(flag)
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
		if resolved == nil && options.Decode && kinds.Normalize(resource) == kinds.Secret && len(indexArgs) > 0 {
			var err error
			resolved, err = resolveParsedExpecting(services.State, refs, kinds.Secret)
			if err != nil {
				return err
			}
		}
		return decodeSecrets(services, resource, resolved, extra, options)
	}

	// The namespace an index fetch is taken in when the user named none.
	var scope string
	if len(refs) > 0 {
		// resolveRefsExpecting resolves every index before any of them is
		// acted on, so an out-of-range index late in the batch is caught
		// before the first kubectl call rather than after some of them have
		// already run. Expecting rather than resolveRefs's plain Resolve: the
		// resource type was named on the command line, so a failure — out of
		// range, no state, or an index left over from a listing of a
		// different kind — is reported against that kind, the way
		// FieldsExpecting always has, instead of generically or silently
		// fetched as whatever the index actually names. Rows of a resource
		// naming several kinds were resolved and checked above.
		if resolved == nil {
			var err error
			resolved, err = resolveParsedExpecting(services.State, refs, kinds.Normalize(resource))
			if err != nil {
				return err
			}
		}
		if spanning && !hasFlag(extra, "--show-kind", "") {
			// Every row names its kind whichever way kubectl replies: it
			// puts the kind in front of a name only when one reply holds
			// several kinds, and an -A listing's indexes are fetched a
			// namespace at a time.
			extra = append(extra, "--show-kind")
		}
		resolved = fetchNames(resolved, spanning)
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
			showListing(services, output, resource, render.AllNamespaces, options.Match, extra)
			return nil
		}

		names := make([]string, 0, len(resolved))
		for _, entry := range resolved {
			names = append(names, entry.Name)
		}
		// The listing's own namespace, unless the user named one. It is the
		// listing's, not the command's, so it is the fetch's Scope rather
		// than a -n in the recorded query (listingScope).
		if extractNamespace(extra) == "" {
			scope = groups[0].Namespace
		}
		extra = append(names, extra...)
	}

	if isWatch(extra) {
		extra = append(extra, replayScope(scope, extra)...)
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
		// Not ahead of a stream another program reads: `kx get ns -w -o name`
		// opened with a line that is not a name.
		if printsTable(extra) {
			render.Caption("watches can't be indexed — streaming kubectl output directly")
		}
		_, err := services.Kubectl.RunInteractive(append([]string{"get", resource}, extra...), false)
		return err
	}

	get := GetCommand{Kubectl: services.Kubectl, State: services.State, Index: services.Index, Scope: scope}
	stop := render.Status("fetching " + resource)
	output, namespace, err := get.Execute(resource, options.Match, extra)
	stop()
	if err != nil {
		return err
	}

	if allNamespaces(extra) {
		namespace = render.AllNamespaces
	}
	// Another cluster's listing is captioned as one, but only as a table:
	// ahead of JSON or names, the caption is a line the reader can't parse.
	crossCluster := clusterFlagIn(extra)
	if crossCluster != "" && printsTable(extra) {
		render.Caption(crossClusterCaption(crossCluster))
	}
	showListing(services, output, resource, namespace, options.Match, extra)
	return nil
}

// showListing prints what kx get fetched: the listing, and under an empty one
// it saved, the way back to the listing it replaced.
//
// A format another program reads gets kubectl's output and nothing of kx's
// on stdout. An empty one is reported on stderr, where kubectl reports it,
// with no way back offered: nothing was saved over the listing behind it (see
// GetCommand.Execute). Nor under any listing kx printed without numbering it
// — another cluster's, a table it cannot place — which replaced nothing
// either. Read off the table, which says so, rather than inferred from the
// flags: inferred, a term emptying a table kx cannot place offered "'kx
// state back' returns to" the entry behind the listing still current.
func showListing(
	services Services, output index.Table, resource, namespace, match string, extra []string,
) {
	if !printsTable(extra) && output.Empty() {
		render.EmptyListingNotice(resource, namespace, match)
		return
	}
	render.IndexedTable(output, resource, namespace)
	if output.Empty() && !output.Unnumbered {
		render.PreviousListingNote(previousListing(services))
	}
}

// previousListing is the entry `kx state back` would return to, for the note an
// empty listing offers.
//
// Read after the listing that found nothing has been saved, not before. The
// entry that was current a moment ago is not always the one behind the new
// one: a listing repeating the query the cursor is already on replaces that
// entry rather than pushing beside it, so `kx get pods` (2 rows) followed by a
// drained `kx get pods` offered "returns to Pods · 2 items" — the entry it had
// just overwritten — and back landed on whatever was before that.
//
// Errors are ignored deliberately: no state yet is the ordinary first-run
// case, and it means there is nothing to offer. So is a cursor at the bottom
// of the stack, where there is nothing behind the current entry; the zero
// State renders no note.
func previousListing(services Services) state.State {
	history, err := services.State.LoadHistory()
	if err != nil || history.Cursor < 1 || history.Cursor >= len(history.States) {
		return state.State{}
	}
	return history.States[history.Cursor-1]
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
