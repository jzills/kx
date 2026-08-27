package cli

import (
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

func (s slotOnly) Save(entry state.State) error { return s.writer.SaveNamed(entry) }

// GetCommand lists resources and saves the listing so later commands can
// resolve indexes against it.
type GetCommand struct {
	Kubectl kubectl.Service
	State   StateWriter
	Index   Indexer
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
	output, err := c.Kubectl.Run(append([]string{"get", resource}, extraArgs...))
	if err != nil {
		return index.Table{}, "", err
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
	if !allNamespaces(extraArgs) && !clusterScoped(resource) {
		namespace = extractNamespace(extraArgs)
		if namespace == "" {
			namespace = c.Kubectl.CurrentNamespace()
		}
	}

	indexed := c.index(output, filterTerm)
	// An index into a spanning listing is only usable if it resolves to a
	// namespace, and the table is the only place that comes from. Asked for a
	// shape that omits the NAMESPACE column — `-o custom-columns=NAME:...` — kx
	// numbered rows it could not place, then resolved every one of them into
	// whatever namespace the caller happened to be standing in and reported the
	// misses as resources that no longer exist. Printing it unnumbered says the
	// same thing kx already says about `-o json`: this is output it cannot index.
	if allNamespaces(extraArgs) && !indexed.Placed() {
		return index.Table{Raw: output}, namespace, nil
	}
	if len(indexed.Entries) > 0 {
		var match *string
		if filterTerm != "" {
			match = &filterTerm
		}
		if extraArgs == nil {
			extraArgs = []string{}
		}
		entry := state.State{
			Resources:     resourcesFrom(indexed.Entries, kinds.Normalize(resource)),
			Namespace:     namespace,
			AllNamespaces: allNamespaces(extraArgs),
			Query: &state.Query{
				Resource: resource,
				Args:     extraArgs,
				Match:    match,
			},
		}
		if err := c.State.Save(entry); err != nil {
			return index.Table{}, "", err
		}
	}
	return indexed, namespace, nil
}

// ExecuteGroups fetches named resources that span namespaces — one kubectl call
// per namespace, since kubectl cannot fetch named resources across namespaces in
// one — and stitches the replies into a single table shaped like the -A listing
// the indexes came from.
//
// Each reply is namespaced, so it arrives without a NAMESPACE column; the column
// is put back from the namespace that call was made for. That is what keeps the
// stitched listing indexable: without it the saved resources would carry no
// namespace and the relisted indexes would resolve no better than the ones they
// replaced.
//
// The entry records no Query. There is no single `kx get` invocation that
// produces this table, and inventing one — the original -A args, say — would
// replay something other than what the entry holds.
func (c GetCommand) ExecuteGroups(
	resource, filterTerm string, groups []namespaceGroup, extraArgs []string,
) (table index.Table, err error) {
	var headers []string
	var merged [][]string
	var raw []string
	// Whether the replies are tables kx can stitch. A non-tabular reply ends
	// the stitching, not the fetching: every namespace the user named still has
	// to be asked for. Returning on the first one answered `kx get pods 1 5 -o
	// yaml` with one namespace's YAML and exit 0, so the resource in the second
	// namespace was silently dropped from a request that named it.
	tabular := true

	for _, group := range groups {
		args := append([]string{"get", resource}, group.Names...)
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

		groupHeaders, rows, _ := index.ParseTable(output)
		if groupHeaders == nil {
			// Non-tabular (-o json/yaml/name). Nothing to index or stitch; the
			// raw replies are printed as they came, the same degradation a
			// non-tabular single-namespace listing already gets.
			tabular = false
			continue
		}
		if filterTerm != "" {
			rows = index.FilterRows(groupHeaders, rows, filterTerm)
		}
		if headers == nil {
			headers = append([]string{"NAMESPACE"}, groupHeaders...)
		}
		for _, row := range rows {
			merged = append(merged, append([]string{group.Namespace}, row...))
		}
	}
	if !tabular || len(merged) == 0 {
		return index.Table{Raw: strings.Join(raw, "\n")}, nil
	}

	indexed := c.Index.AddRows(headers, merged)
	if len(indexed.Entries) > 0 {
		if err := c.State.Save(state.State{
			// Groups are fetched one namespace at a time and stitched back
			// together, so the merged listing spans them by construction.
			Resources:     resourcesFrom(indexed.Entries, kinds.Normalize(resource)),
			AllNamespaces: true,
		}); err != nil {
			return index.Table{}, err
		}
	}
	return indexed, nil
}

// index parses kubectl output once and numbers it, narrowing by name first
// when a --match term was given.
//
// Filtering happens before numbering so the indexes run 1..n over the rows the
// user is actually looking at, and on the rows themselves so nothing has to be
// re-serialised to text and parsed again between the two.
func (c GetCommand) index(output, filterTerm string) index.Table {
	if filterTerm == "" {
		return c.Index.Add(output)
	}
	headers, rows, _ := index.ParseTable(output)
	if headers == nil {
		return c.Index.Add(output)
	}
	return c.Index.AddRows(headers, index.FilterRows(headers, rows, filterTerm))
}

// resourcesFrom turns indexed entries into saved resources of a single kind,
// carrying each row's namespace through when the listing reported one.
func resourcesFrom(entries []index.Entry, kind kinds.Kind) state.Resources {
	resources := make([]state.Resource, 0, len(entries))
	for _, entry := range entries {
		resources = append(resources, state.Resource{
			Name: entry.Name, Kind: kind, Namespace: entry.Namespace,
		})
	}
	return state.NewOrderedResources(resources)
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
