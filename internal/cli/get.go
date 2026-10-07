package cli

import (
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// Indexer prefixes kubectl output with an index column and filters it by name.
type Indexer interface {
	// Add parses kubectl output and numbers it, for callers holding text.
	Add(output string) index.Table
	// AddRows numbers rows already parsed, for callers that narrowed or
	// widened the table on the way and must not re-serialise it to do so.
	AddRows(headers []string, rows [][]string) index.Table
	// Parse reads kubectl table output into a listing, to be narrowed and
	// numbered as steps of their own (see index.Listing).
	Parse(output string) (index.Listing, bool)
	// Stitch is Parse for tables already parsed — several replies stitched
	// into one listing, numbered on from one table into the next.
	Stitch(tables []index.RawTable) (index.Listing, bool)
}

// StateWriter is the slice of the state service `get` needs.
type StateWriter interface {
	Save(state.State) error
}

// NamedStateWriter writes a listing to its per-kind slot instead of the history
// stack.
type NamedStateWriter interface {
	SaveNamed(state.State) error
}

// slotOnly routes a listing into its per-kind slot, leaving the history stack
// untouched.
//
// `kx ns` runs through GetCommand like any other listing, but must not push an
// entry: switching namespaces is the most frequent thing kx does, and stacking
// every listing evicted the work the stack exists for. Swapping the writer
// rather than teaching GetCommand about kinds keeps that decision at the one
// call site that makes it.
type slotOnly struct{ writer NamedStateWriter }

// A listing that found nothing is not written to the slot. A slot is filed
// under the one kind it holds, and a listing holding nothing names no kind, so
// SaveNamed refuses it — which reached the user as "state: a slot needs a
// single-kind listing" where `kx ns` should have said none were found. The
// slot keeps what it had, exactly as `kx contexts` has always left it.
//
// Unlike the history stack, a stale slot cannot resolve an index into the
// wrong kind: `kx ns 2` reads the Namespace slot and nothing else, so the
// worst it names is a namespace that has since gone, which relisting reports.
func (s slotOnly) Save(entry state.State) error {
	if entry.Resources.Len() == 0 {
		return nil
	}
	return s.writer.SaveNamed(entry)
}

// GetCommand lists resources and saves the listing so later commands can
// resolve indexes against it.
type GetCommand struct {
	Kubectl kubectl.Service
	State   StateWriter
	Index   Indexer
	// Scope is the namespace a listing whose arguments name none is taken
	// in: a refresh's, which replays a listing where it was first taken.
	// Empty takes it in the current namespace, as kx get does. The query is
	// saved as typed either way, so the refresh replaces the stale entry.
	Scope string
}

// replayScope is the -n a listing is taken under when its arguments name no
// scope of their own: Scope's, if any.
func replayScope(scope string, extraArgs []string) []string {
	if scope == "" || scopeFlagIn(extraArgs) != "" {
		return nil
	}
	return []string{"-n", scope}
}

// extractNamespace finds an explicit namespace in the pass-through flags, so
// the saved state records the namespace the listing actually came from rather
// than the context's current one.
// The spellings themselves are extractString's business rather than this
// function's. A second matcher living here drifted from that one: `-nprod` and
// `-n=prod` went unrecognised, so the state recorded the current namespace
// while kubectl listed the one that was asked for, and every index afterwards
// resolved against the wrong namespace.
//
// The flag stays in extraArgs for kubectl, so the stripped remainder is
// discarded; extractString builds a new slice and never mutates its input. So
// is the error, which fires only when the flag ends the argv with no value —
// kubectl rejects that before any listing reaches the state.
func extractNamespace(extraArgs []string) string {
	namespace, _, _ := extractString(extraArgs, "--namespace", "-n")
	return namespace
}

func allNamespaces(extraArgs []string) bool {
	present, _ := extractBool(extraArgs, "--all-namespaces", "-A")
	return present
}

// isWatch reports whether the pass-through flags ask kubectl to stream rather
// than return a completed listing.
func isWatch(extraArgs []string) bool {
	present, _ := extractBool(extraArgs, "--watch", "-w", "--watch-only")
	return present
}

// wantsLiveTable reports whether the pass-through flags request kubectl's
// default or wide table shape — the shape runWatch's live-redrawing table
// applies to. Non-tabular -o formats keep the raw-streaming passthrough
// instead, since a themed table doesn't apply to non-tabular output. -A is
// included: watchRows keys rows by NAMESPACE/NAME when a NAMESPACE column is
// present, so same-named pods in different namespaces don't collide.
func wantsLiveTable(extraArgs []string) bool {
	output, _, _ := extractString(extraArgs, "--output", "-o")
	return output == "" || output == "wide"
}

// printsTable reports whether the pass-through flags leave kubectl printing a
// table, which a caption can head. Anything else — JSON, YAML, names, a
// template — is read by another program as often as by a person, and a line
// of prose ahead of it breaks the reading: `kx get ns --context=b -o json | jq`
// failed to parse. A format kubectl adds later counts as not a table, since a
// missing caption is a smaller failure than corrupted output.
func printsTable(extraArgs []string) bool {
	output, _, _ := extractString(extraArgs, "--output", "-o")
	format, _, _ := strings.Cut(output, "=")
	switch format {
	case "", "wide", "custom-columns", "custom-columns-file":
		return true
	}
	return false
}

// getArgs begins a kubectl get of resource, with args the arguments that
// follow it.
//
// Rows of a listing of several kinds fetched again by index lead those
// arguments named Kind/name, the one spelling in which kubectl takes several
// kinds beside names, and kubectl refuses a resource type beside it. The
// resource is left out of the call, though not out of the listing: kx get all
// 2 3 is still a listing of all, which is how its caption, kx state and the
// command to relist it name it. Read off the arguments rather than passed
// along, so a stale one is replayed from what its entry records.
//
// An empty resource is such a fetch saved before it recorded the resource.
func getArgs(resource string, args []string) []string {
	if resource == "" || kinds.Several(resource) && len(args) > 0 && namesKind(args[0]) {
		return []string{"get"}
	}
	return []string{"get", resource}
}

// namesKind reports whether a kubectl get argument is a Kind/name rather than
// a flag or a bare name.
func namesKind(arg string) bool {
	return !strings.HasPrefix(arg, "-") && strings.Contains(arg, "/")
}

// Execute runs `kubectl get`, indexes the output and persists it. It returns
// the text to display and the namespace the listing came from.
//
// The namespace is returned rather than left for the caller to read back out of
// saved state: an empty listing saves nothing, so a caller doing that would
// caption it with whatever the previous entry's namespace was. Switching to an
// empty namespace and running `kx get pods` reported the namespace you left.
func (c GetCommand) Execute(
	resource, filterTerm string, extraArgs []string,
) (table index.Table, namespace string, err error) {
	// A cluster-scoped kind is listed in no namespace, replayed or not.
	scope := replayScope(c.Scope, extraArgs)
	if clusterScoped(string(listingKind(resource))) {
		scope = nil
	}
	args := append(getArgs(resource, extraArgs), extraArgs...)
	output, err := c.Kubectl.Run(append(args, scope...))
	if err != nil {
		return index.Table{}, "", err
	}
	// Another cluster's listing is printed and never saved. Saved, its rows
	// were stamped with this context and namespace, so `kx get pods
	// --context=b` then `kx delete 1` deleted a same-named pod here. Its
	// namespace is only the one named, if any: the current one is this
	// cluster's, not that one's.
	if clusterFlagIn(extraArgs) != "" {
		return unnumberedListing(output, filterTerm), extractNamespace(extraArgs), nil
	}
	// An -A listing has no single namespace to record on the entry; each
	// resource carries its own instead, read from the table's NAMESPACE column.
	// The caller labels the scope.
	//
	// A cluster-scoped kind has none to record either, for a different reason:
	// there is no namespace to be in. Left to default, kx stamped whichever one
	// the caller happened to be standing in onto every Node, PersistentVolume,
	// StorageClass and CRD it listed, then printed it back as though it meant
	// something — "Nodes · diagnostics · 1 item". Empty is what Caption already
	// drops, so the scope segment disappears rather than lying.
	//
	// This is display and saved state only. kubectl ignores -n for a
	// cluster-scoped resource, and accepts an empty one, so the commands that
	// resolve these indexes need no change.
	if !allNamespaces(extraArgs) && !clusterScoped(string(listingKind(resource))) {
		namespace = extractNamespace(extraArgs)
		if namespace == "" && scope != nil {
			namespace = c.Scope
		}
		if namespace == "" {
			namespace = c.Kubectl.CurrentNamespace()
		}
	}

	listing, tabular := c.Index.Parse(output)
	// A table kx cannot number is printed unnumbered, narrowed by the term
	// all the same (see numberable).
	if tabular && !numberable(listing, resource, extraArgs) {
		return unnumbered(listing, output, filterTerm), namespace, nil
	}
	// Output kx cannot number leaves the current listing alone: `-o json`,
	// `-o yaml` and `-o name` are printed as they arrived, and the numbers on
	// screen still belong to the listing before them. Saved as an entry
	// holding nothing, it wiped them — `kx get pods` then `kx get pods -o
	// json` left `kx ref 1` reporting an empty listing the user never asked
	// for.
	//
	// Not a table kx can read, rather than a table with no rows in it: the
	// empty listing below has no header either (kubectl puts "No resources
	// found" on stderr), and it is a fact about the cluster that the indexes
	// must follow. What separates them is whether anything came back at all —
	// in a table format. An empty -o name or template reply is no more a
	// listing than a full one: saved, kx get pods -l app=x -o name replaced
	// the listing behind it, where one that found pods left it alone.
	if !tabular {
		raw := index.Table{Raw: output, Match: filterTerm, Unnumbered: true}
		if !raw.Empty() || !printsTable(extraArgs) {
			return raw, namespace, nil
		}
	}
	indexed := listing.Narrow(filterTerm).Number()
	indexed.Raw = output
	indexed.Match = filterTerm
	// Saved unconditionally, including when the listing found nothing. An
	// empty listing that saved no entry left the *previous* listing resolving
	// indexes: `kx get pods -n a` (14 rows), `kx get pods -n b` (none), then
	// `kx delete 1` deleted a pod in a — a namespace and two commands away
	// from anything the screen had shown. The entry carries its query, so the
	// refusal it produces can name what found nothing.
	if err := c.State.Save(getListing(resource, filterTerm, extraArgs, namespace, indexed.Entries)); err != nil {
		return index.Table{}, "", err
	}
	return indexed, namespace, nil
}

// unnumberedListing is output kx prints but does not index, narrowed by
// filterTerm as a numbered listing would be. Output that is not a table, or
// that found nothing, is carried as it came.
func unnumberedListing(output, filterTerm string) index.Table {
	if filterTerm == "" {
		return index.Table{Raw: output, Unnumbered: true}
	}
	listing, ok := index.ParseListing(output)
	if !ok {
		if strings.TrimSpace(output) == "" {
			return index.Table{Match: filterTerm, Unnumbered: true}
		}
		return index.Table{Raw: output, Unnumbered: true}
	}
	return unnumbered(listing, output, filterTerm)
}

// unnumbered is a parsed listing kx prints but does not index: text, as it
// came, when there is no term, and otherwise the listing narrowed by
// filterTerm — every matching row of it, since nothing here is numbered (see
// index.Listing) — or, when the term matched none, an empty listing that
// names it.
func unnumbered(listing index.Listing, text, filterTerm string) index.Table {
	if filterTerm == "" {
		return index.Table{Raw: text, Unnumbered: true}
	}
	narrowed := listing.Narrow(filterTerm)
	if narrowed.Empty() {
		return index.Table{Match: filterTerm, Unnumbered: true}
	}
	return index.Table{Raw: narrowed.Unnumbered(), Unnumbered: true}
}

// numberable reports whether kx can number a listing kubectl replied with to
// resource and args — read off the whole reply, before any term narrows it.
// Whether a listing is numbered is a fact about the reply, so the same
// command is numbered, or printed unnumbered, alike with -m and without.
// Decided from the rows a term left, it was not: kx get pods -A -o
// custom-columns=NAME:.metadata.name -m zzz left no row to find unplaced and
// was saved, empty, over the listing behind it, where -m redis left that
// listing alone.
//
// Two shapes cannot be numbered:
//
//   - An -A listing that does not say where its rows live (index.Listing.
//     Placed). An index into one resolves only through the namespace its row
//     records; numbered anyway, every index resolved into whatever namespace
//     the caller stood in, and the misses were reported as resources that no
//     longer exist. A reply with no rows has nothing to place, and is an
//     empty listing like any other.
//   - Several kinds whose rows do not name their kind (custom columns).
//     kubectl's kind/name is the only place a row's kind comes from.
func numberable(listing index.Listing, resource string, args []string) bool {
	if allNamespaces(args) && !listing.Empty() && !listing.Placed() {
		return false
	}
	if kinds.Several(resource) && !namesCarryKinds(listing.Names()) {
		return false
	}
	return true
}

// namesCarryKinds reports whether every row of a listing of several kinds is
// named kind/name, which is what kubectl prints for one.
func namesCarryKinds(names []string) bool {
	for _, name := range names {
		if !strings.Contains(name, "/") {
			return false
		}
	}
	return true
}

// listingKind is the kind of a listing's rows when the rows don't name their
// own: the argument's, or for type/name the type's.
func listingKind(resource string) kinds.Kind {
	if kind, _, named := strings.Cut(resource, "/"); named {
		return kinds.Normalize(kind)
	}
	return kinds.Normalize(resource)
}

// getListing is the entry `kx get <resource>` saves for a listing: its rows as
// resources of the one kind, the namespace it came from, and the query that
// replays it. Shared with the MCP server's list_resources, which saves the same
// listing when --write-listings is on, so the two can only ever record one
// shape. They dedupe against each other only when the queries match exactly:
// list_resources always pins `-n <ns>` into the query and a plain `kx get
// pods` saves no args, so the agent's listing usually pushes beside the
// user's. When they do match (`kx get pods -n default`), the agent's entry
// replaces the user's, Source and all.
func getListing(
	resource, filterTerm string, extraArgs []string, namespace string, entries []index.Entry,
) state.State {
	if extraArgs == nil {
		extraArgs = []string{}
	}
	return state.State{
		Resources:     resourcesFrom(entries, listingKind(resource)),
		Namespace:     namespace,
		AllNamespaces: allNamespaces(extraArgs),
		Query: &state.Query{
			Resource: resource,
			Args:     extraArgs,
			Match:    matchOf(filterTerm),
		},
	}
}

// ExecuteGroups fetches named resources that span namespaces — one kubectl call
// per namespace, since kubectl cannot fetch named resources across namespaces in
// one — and stitches the replies into a listing shaped like the -A listing the
// indexes came from: one table, or one per kind when they span kinds.
//
// Each reply is namespaced, so it arrives without a NAMESPACE column; the column
// is put back from the namespace that call was made for. That is what keeps the
// stitched listing indexable: without it the saved resources would carry no
// namespace and the relisted indexes would resolve no better than the ones they
// replaced.
//
// The entry's Query is a CommandFetch, which is never run again. There is no
// single `kx get` invocation that produces this table, and inventing one — the
// original -A args, say — would replay something other than what the entry
// holds. It records the kind and the term all the same, which an empty
// listing is captioned with.
func (c GetCommand) ExecuteGroups(
	resource, filterTerm string, groups []namespaceGroup, extraArgs []string,
) (table index.Table, err error) {
	// The stitched tables, one per kind, in the order each was first seen.
	var tables []index.RawTable
	at := map[string]int{}
	var raw []string
	// Whether the replies are tables kx can stitch. A non-tabular reply ends
	// the stitching, not the fetching: every namespace the user named still has
	// to be asked for. Returning on the first one answered `kx get pods 1 5 -o
	// yaml` with one namespace's YAML and exit 0, so the resource in the second
	// namespace was silently dropped from a request that named it.
	tabular := true

	for _, group := range groups {
		args := append(getArgs(resource, group.Names), group.Names...)
		args = append(args, "-n", group.Namespace)
		args = append(args, extraArgs...)
		output, err := c.Kubectl.Run(args)
		if err != nil {
			return index.Table{}, err
		}
		raw = append(raw, output)
		if !tabular {
			continue
		}

		replies, ok := index.ParseTables(output)
		if !ok {
			// Non-tabular (-o json/yaml/name). Nothing to index or stitch; the
			// raw replies are printed as they came, the same degradation a
			// non-tabular single-namespace listing already gets.
			tabular = false
			continue
		}
		// Each kind's rows join its own table, whichever namespace they came
		// from: rows of several kinds — kx get all's — laid under one header
		// put a Service's TYPE under a Deployment's READY.
		for _, reply := range replies {
			key := strings.Join(reply.Headers, "\x00")
			position, seen := at[key]
			if !seen {
				position = len(tables)
				at[key] = position
				tables = append(tables, index.RawTable{
					Headers: append([]string{"NAMESPACE"}, reply.Headers...),
				})
			}
			for _, row := range reply.Rows {
				tables[position].Rows = append(tables[position].Rows, append([]string{group.Namespace}, row...))
			}
		}
	}
	// No tables means no reply was a table at all. Tables with no rows the
	// term leaves is a term that matched none of them, which is a listing
	// like any other: the raw replies it used to fall back to were kubectl's
	// unfiltered tables, printing exactly the rows the term had excluded.
	listing, stitched := c.Index.Stitch(tables)
	if !tabular || !stitched {
		return index.Table{Raw: strings.Join(raw, "\n"), Unnumbered: true}, nil
	}
	// Rows of several kinds whose names carry no kind cannot be numbered, as
	// in Execute — decided, as there, from every row stitched, before the term
	// narrows them. Here a namespace holding one of the kinds answers with
	// bare names whenever --show-kind is not in force, and saved, every row's
	// kind was the argument's. Printed stitched, under the namespaces put
	// back. No -A among args: each namespace is fetched with its own -n, and
	// the NAMESPACE column put back places every row.
	if !numberable(listing, resource, extraArgs) {
		return unnumbered(listing, listing.Unnumbered(), filterTerm), nil
	}

	indexed := listing.Narrow(filterTerm).Number()
	indexed.Match = filterTerm
	// Saved even when the term left nothing, as GetCommand.Execute saves an
	// empty listing: otherwise the -A listing these indexes came from stays
	// current behind a screen that shows none of it.
	if err := c.State.Save(state.State{
		// Groups are fetched one namespace at a time and stitched back
		// together, so the merged listing spans them by construction.
		Resources:     resourcesFrom(indexed.Entries, listingKind(resource)),
		AllNamespaces: true,
		Query: &state.Query{
			Command: state.CommandFetch, Resource: resource, Args: []string{}, Match: matchOf(filterTerm),
		},
	}); err != nil {
		return index.Table{}, err
	}
	return indexed, nil
}

// resourcesFrom turns indexed entries into saved resources, carrying each
// row's namespace through when the listing reported one.
//
// A row is of kind unless it names its own: a listing of several kinds names
// every row kind/name — deployment.apps/web — and saved whole under the
// argument, it resolved to deploy,svc/deployment.apps/web, which kubectl
// rejects. A name never holds a "/", so one in a row is that prefix.
func resourcesFrom(entries []index.Entry, kind kinds.Kind) state.Resources {
	resources := make([]state.Resource, 0, len(entries))
	for _, entry := range entries {
		resource := state.Resource{Name: entry.Name, Kind: kind, Namespace: entry.Namespace}
		if prefix, name, named := strings.Cut(entry.Name, "/"); named {
			resource.Name, resource.Kind = name, kinds.Qualified(prefix)
		}
		resources = append(resources, resource)
	}
	return state.NewOrderedResources(resources)
}

// scopeFlagIn reports the namespace-scope flag present in argv, spelled the way
// it was typed so a refusal quotes back what the user actually wrote, or "" when
// there is none.
//
// -A's presence is read through extractBool rather than by scanning for the
// token, so "--all-namespaces=false" — a request for the ordinary
// single-namespace listing, not for a scope — is correctly absent.
func scopeFlagIn(args []string) string {
	if present, _ := extractBool(args, "--all-namespaces", "-A"); present {
		return firstSpelling(args, "--all-namespaces", "-A")
	}
	if hasFlag(args, "--namespace", "-n") {
		return firstSpelling(args, "--namespace", "-n")
	}
	return ""
}

// firstSpelling names which of several spellings of one flag appears first in
// argv, without whatever value was attached to it. The attached-value form is
// recognised for shorthands only ("-nprod"), matching hasFlag.
func firstSpelling(args []string, names ...string) string {
	for _, arg := range args {
		for _, name := range names {
			attachedShorthand := len(name) == 2 &&
				len(arg) > len(name) && strings.HasPrefix(arg, name)
			if arg == name || strings.HasPrefix(arg, name+"=") || attachedShorthand {
				return name
			}
		}
	}
	return names[0]
}

// clusterScopedScopeError refuses a namespace flag on a kind that has no
// namespace for it to name.
//
// Refused rather than forwarded, which is the same call kx already makes for a
// scope flag beside an index: kubectl accepts both and answers about something
// other than what was asked. Silently forwarding -A was worse here than
// meaningless — kubectl returns a table with no NAMESPACE column, which is the
// shape GetCommand.Execute treats as unplaceable, so the listing printed
// unnumbered and saved nothing while the previous listing's indexes stayed
// live underneath it.
func clusterScopedScopeError(flag, resource string) error {
	return fmt.Errorf(
		"'%s' cannot be combined with %s — they live outside any namespace, so "+
			"there is no scope to set. Drop the flag.",
		flag, kinds.PluralDisplay(resource))
}

// unsupportedKindError refuses a command for the kind an index resolved to,
// naming both what was selected and what the command does work on.
//
// Both halves matter, and the codebase had each of them without the other.
// "scale is not supported for 'Pod'." said what you picked but not what to
// pick instead; "cp is only supported for pods." said the opposite and left
// you to work out what index 1 had been. An index is a number, so the kind it
// resolved to is precisely the fact the user does not have in front of them —
// and "then which kinds does this work on" is the question the first form
// always provoked.
// Phrased "kx X does not support" rather than "X is not supported for",
// which is what most of these said, because one command is named for a plural:
// "logs is not supported" is wrong and "logs are not supported" cannot be
// generated from the same template as "scale is". Naming the command as kx
// spells it takes the copula out of the sentence and reads correctly for all
// twelve.
func unsupportedKindError(command string, kind kinds.Kind, supported kinds.Set) error {
	return fmt.Errorf("kx %s does not support '%s' — only %s.",
		command, kind, supported.List())
}

// clusterScoped reports whether a resource spelling names a kind that lives
// outside any namespace.
//
// A kind whose scope is unknown — a CRD on a machine with no discovery cache —
// is treated as namespaced, which is what kx did for every kind before it
// could tell. Guessing the other way would strip the namespace off a
// namespaced CRD and leave every index resolving into the wrong place; being
// wrong about a caption is the cheaper of the two failures.
func clusterScoped(resource string) bool {
	namespaced, known := kinds.Namespaced(kinds.Normalize(resource))
	return known && !namespaced
}
