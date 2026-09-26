package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/tree"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMCPTools adds every tool the server offers. One function, so the
// public surface can be read in one place — and every handler but scan's goes
// through serialized, so no two calls ever run at once (see mcpDeps.mu).
func registerMCPTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_marks",
		Description: "List the kx marks: names the user pinned to resources, usable as a target's mark.",
		Annotations: readOnlyTool("List marks"),
	}, serialized(deps, deps.listMarks))
	mcp.AddTool(server, &mcp.Tool{
		Name: "mark",
		Description: "Pin a name to a resource so the user can reach it as @name in kx, e.g. to hand " +
			"back the resource you found at fault. The target may itself be a mark or an index from the " +
			"user's listing, giving that resource a second name. Refuses a name that is already a mark, and " +
			"a kind, name or namespace that is not shaped like one (a leading '-' reads as a kubectl flag). " +
			"Writes kx's local state only, never the cluster.",
		Annotations: &mcp.ToolAnnotations{Title: "Mark a resource", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, serialized(deps, deps.mark))
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_resources",
		Description: "List resources of one kind by name and namespace — the names the other tools take. " +
			"Defaults to the current namespace. When kx mcp runs with --write-listings, the listing is " +
			"saved to the user's kx history, as kx get would save it, and each row carries its index there.",
		Annotations: listingTool("List resources", deps.WriteListings),
	}, serialized(deps, deps.listResources))
	mcp.AddTool(server, &mcp.Tool{
		Name: "diagnose",
		Description: "Diagnose Kubernetes health. With a target, analyses that resource — replica counts, " +
			"container states, restarts, warning events — and returns findings ranked most specific first. " +
			"With no target, sweeps a namespace (or every namespace) and returns the unhealthy resources. " +
			"Supports Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, " +
			"Ingress, Pod and Node. When kx mcp runs with --write-listings, a sweep is saved to the user's " +
			"kx history, every swept resource in the order returned, and each carries its index there — so " +
			"without full, the numbers skip the healthy rows left out.",
		Annotations: listingTool("Diagnose", deps.WriteListings),
	}, serialized(deps, deps.diagnose))
	// Registered with an untyped output: a tree node's children are tree
	// nodes, and the SDK's schema inference refuses a recursive type.
	mcp.AddTool[treeInput, any](server, &mcp.Tool{
		Name: "tree",
		Description: "Show ownership: what a resource owns and is owned by (Deployment → ReplicaSet → Pod → " +
			"containers), or the whole ownership forest of a namespace when there is no target. Large graphs " +
			"are cut breadth-first at limit nodes; truncated says how many were left out. When kx mcp runs " +
			"with --write-listings, the whole walk is saved to the user's kx history, as kx tree would save " +
			"it, and each node but a container or a namespace root carries its index there; a limit cuts " +
			"what is returned, not what is saved.",
		Annotations: listingTool("Ownership tree", deps.WriteListings),
	}, serialized(deps, deps.tree))
	registerEvidenceTools(server, deps)
	mcp.AddTool(server, &mcp.Tool{
		Name: "top",
		Description: "Current CPU and memory usage of pods (percent of their limits) or nodes " +
			"(percent of capacity), from metrics-server. When kx mcp runs with --write-listings, the " +
			"listing is saved to the user's kx history, as kx top would save it, and each row carries " +
			"its index there.",
		Annotations: listingTool("Top", deps.WriteListings),
	}, serialized(deps, deps.top))
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_yaml",
		Description: "A resource's manifest as YAML, optionally narrowed to named top-level or " +
			"nested keys (fields). Secret values and the last-applied annotation are redacted.",
		Annotations: readOnlyTool("Get YAML"),
	}, serialized(deps, deps.getYAML))
	// Not through serialized: a sweep can take minutes, so scan holds the
	// lock only while it resolves images — see registerScanTool.
	registerScanTool(server, deps)
}

// readOnlyTool annotates a tool that reads the cluster and writes nothing.
func readOnlyTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

// scopeConflict refuses a namespace named beside allNamespaces — the MCP
// spelling of the refusal kx get and kx diag make for -n beside -A.
func scopeConflict(namespace string, allNamespaces bool) error {
	if namespace != "" && allNamespaces {
		return errors.New("'allNamespaces' and 'namespace' cannot be combined.")
	}
	return nil
}

// listingTool annotates a tool that lists: read-only, like readOnlyTool,
// unless kx mcp runs with --write-listings, when each call saves its listing
// to kx's local state and so must not claim to change nothing. Not
// destructive, said explicitly because the MCP default turns true once a tool
// is not read-only: a save only pushes onto the history, as a kx get would.
// Idempotent still — the same listing twice replaces rather than pushes.
func listingTool(title string, writeListings bool) *mcp.ToolAnnotations {
	if !writeListings {
		return readOnlyTool(title)
	}
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(false), IdempotentHint: true}
}

func boolPtr(value bool) *bool { return &value }

// clampLimit resolves a "how many" argument against the default it takes
// when absent and the cap it may not exceed: a non-positive value (an
// omitted field decodes to zero) takes def, and anything larger is capped at
// max. Shared by every tool that bounds a listing — list_resources, tree,
// events and logs's tail — so the four don't drift into four different
// definitions of "unset".
func clampLimit(requested, def, max int) int {
	if requested <= 0 {
		return def
	}
	return min(requested, max)
}

// mcpMark is one mark as a tool reports it. Resource, not Name, holds the
// resource's name: Name is the mark's own.
type mcpMark struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Context   string `json:"context,omitempty"`
	ByAgent   bool   `json:"byAgent,omitempty" jsonschema:"True when an agent took this mark with the mark tool rather than the user with kx mark."`
}

func mcpMarkOf(name string, mark state.Mark) mcpMark {
	return mcpMark{
		Name: name, Kind: string(mark.Kind), Resource: mark.Name,
		Namespace: mark.Namespace, Context: mark.Context,
		ByAgent: mark.Source == state.SourceMCP,
	}
}

type listMarksOutput struct {
	Context string    `json:"context"`
	Marks   []mcpMark `json:"marks"`
}

func (d mcpDeps) listMarks(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, listMarksOutput, error) {
	marks, err := d.State.Marks()
	if err != nil {
		return nil, listMarksOutput{}, err
	}
	out := listMarksOutput{Context: d.Kubectl.CurrentContext(), Marks: make([]mcpMark, 0, len(marks))}
	for name, mark := range marks {
		out.Marks = append(out.Marks, mcpMarkOf(name, mark))
	}
	sort.Slice(out.Marks, func(i, j int) bool { return out.Marks[i].Name < out.Marks[j].Name })
	return nil, out, nil
}

type markInput struct {
	Name   string    `json:"name" jsonschema:"The mark's name: letters, digits, '-', '_' and '.', not a bare number. A leading @ is dropped."`
	Target mcpTarget `json:"target" jsonschema:"The resource to mark."`
}

type markOutput struct {
	Context string  `json:"context"`
	Mark    mcpMark `json:"mark"`
}

func (d mcpDeps) mark(_ context.Context, _ *mcp.CallToolRequest, in markInput) (*mcp.CallToolResult, markOutput, error) {
	// Read once, before the target is resolved or checked: a switch while
	// that is in flight must not file this cluster's resource under the next.
	kubeContext := d.Kubectl.CurrentContext()
	name := strings.TrimPrefix(in.Name, "@")
	// Before validMarkName, whose empty-name refusal is the CLI's and
	// suggests an index.
	if strings.TrimSpace(name) == "" {
		return nil, markOutput{}, errors.New("A mark needs a name — give one such as 'culprit'.")
	}
	if err := validMarkName(name); err != nil {
		return nil, markOutput{}, err
	}
	// Advisory, and lock-free: a taken name is refused before the target is
	// resolved, so that refusal wins over any target error and costs no
	// kubectl round trip. SaveMarkIfAbsent below is what actually decides —
	// the name can still be taken between here and there.
	marks, err := d.State.Marks()
	if err != nil {
		return nil, markOutput{}, err
	}
	if existing, ok := marks[name]; ok {
		return nil, markOutput{}, markTakenError(name, existing)
	}
	target, err := d.resolveTarget(in.Target)
	if err != nil {
		return nil, markOutput{}, err
	}
	// kx mark only ever pins a resource it has just listed. Nothing here was
	// listed, so ask once that it exists: a mark on nothing would surface
	// later as a NotFound the user never caused.
	if _, err := d.Kubectl.Run(target.getArgs("-o", "name")); err != nil {
		return nil, markOutput{}, err
	}
	mark := state.Mark{
		Resource: state.Resource{Name: target.Name, Kind: target.Kind, Namespace: target.Namespace},
		Context:  kubeContext,
		Source:   state.SourceMCP,
	}
	// One lock hold for the check and the write: a mark the user adds from a
	// terminal between a separate check and SaveMark would otherwise be moved.
	existing, err := d.State.SaveMarkIfAbsent(name, mark)
	if err != nil {
		return nil, markOutput{}, err
	}
	if existing != nil {
		return nil, markOutput{}, markTakenError(name, *existing)
	}
	return nil, markOutput{Context: mark.Context, Mark: mcpMarkOf(name, mark)}, nil
}

// markTakenError refuses a name that is already a mark.
func markTakenError(name string, existing state.Mark) error {
	return fmt.Errorf(
		"@%s already marks %s/%s — marks belong to the user, so this tool never moves one. Choose another name.",
		name, existing.Kind, existing.Name)
}

const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

type listInput struct {
	Kind          string `json:"kind" jsonschema:"Resource type as kubectl spells it: pods, deploy, svc, nodes, a CRD's name. One type."`
	Namespace     string `json:"namespace,omitempty" jsonschema:"Namespace to list; defaults to the current namespace."`
	AllNamespaces bool   `json:"allNamespaces,omitempty" jsonschema:"List across every namespace."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Most rows to return; default 200, at most 1000. total always counts every row."`
}

type listedResource struct {
	// Index is the row's position in the saved listing; absent unless kx mcp
	// runs with --write-listings, when there is a saved listing to be in.
	Index     int    `json:"index,omitempty" jsonschema:"This row's number in the user's kx history, present only when kx mcp runs with --write-listings; the user can spend it in kx."`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type listOutput struct {
	Context       string           `json:"context"`
	Kind          string           `json:"kind"`
	Namespace     string           `json:"namespace,omitempty"`
	AllNamespaces bool             `json:"allNamespaces,omitempty"`
	Total         int              `json:"total"`
	Resources     []listedResource `json:"resources"`
}

func (d mcpDeps) listResources(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, listOutput, error) {
	if strings.ContainsAny(in.Kind, ",/") || strings.EqualFold(in.Kind, "all") {
		return nil, listOutput{}, fmt.Errorf("'%s' is not one resource type — list one kind per call.", in.Kind)
	}
	if err := validKind(in.Kind); err != nil {
		return nil, listOutput{}, err
	}
	if in.Namespace != "" {
		if err := validNamespace(in.Namespace); err != nil {
			return nil, listOutput{}, err
		}
	}
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, listOutput{}, err
	}
	kind := kinds.Normalize(in.Kind)
	// Context is kx's own pseudo-kind for kubeconfig contexts, not something
	// kubectl lists — and a saved listing of it would overwrite the Context
	// slot that `kx ctx N` reads.
	if strings.EqualFold(string(kind), string(kinds.Context)) {
		return nil, listOutput{}, errors.New("'Context' is kx's name for kubeconfig contexts, not a resource type — list a Kubernetes kind.")
	}
	isClusterScoped := clusterScoped(in.Kind)
	if isClusterScoped && (in.Namespace != "" || in.AllNamespaces) {
		flag := "namespace"
		if in.AllNamespaces {
			flag = "allNamespaces"
		}
		return nil, listOutput{}, clusterScopedScopeError(flag, in.Kind)
	}

	// The context before kubectl runs: it labels the output and stamps the
	// saved listing, so a switch mid-call cannot file these rows under the
	// next context (see listingSave).
	current := d.Kubectl.CurrentContext()
	args := []string{"get", in.Kind}
	namespace := ""
	switch {
	case isClusterScoped:
	case in.AllNamespaces:
		args = append(args, "-A")
	default:
		namespace = in.Namespace
		if namespace == "" {
			namespace = d.Kubectl.CurrentNamespace()
		}
		args = append(args, "-n", namespace)
	}
	output, err := d.Kubectl.Run(args)
	if err != nil {
		return nil, listOutput{}, err
	}
	table := index.Service{}.Add(output)
	if !table.Indexable() && strings.TrimSpace(output) != "" {
		return nil, listOutput{}, fmt.Errorf("kubectl's listing of %s has no NAME column to read names from.", in.Kind)
	}

	// Saved as `kx get <kind>` saves it, and under the same rules: an empty
	// listing is saved, since it is the listing now, and an -A table whose rows
	// cannot be placed is not — GetCommand prints that one unnumbered, and an
	// index into it would resolve in whatever namespace the user stands in.
	indexed := d.indexed()
	if indexed && in.AllNamespaces && len(table.Entries) > 0 && !table.Placed() {
		indexed = false
	}
	if indexed {
		if err := d.listingSave(current)(getListing(in.Kind, "", args[2:], namespace, table.Entries)); err != nil {
			return nil, listOutput{}, err
		}
	}

	limit := clampLimit(in.Limit, defaultListLimit, maxListLimit)
	out := listOutput{
		Context: current, Kind: string(kind), Namespace: namespace,
		AllNamespaces: in.AllNamespaces, Total: len(table.Entries),
		Resources: make([]listedResource, 0, min(len(table.Entries), limit)),
	}
	for position, entry := range table.Entries[:min(len(table.Entries), limit)] {
		rowNamespace := namespace
		if entry.Namespace != "" {
			rowNamespace = entry.Namespace
		}
		row := listedResource{Kind: string(kind), Name: entry.Name, Namespace: rowNamespace}
		if indexed {
			row.Index = position + 1
		}
		out.Resources = append(out.Resources, row)
	}
	return nil, out, nil
}

type diagnoseInput struct {
	Target        *mcpTarget `json:"target,omitempty" jsonschema:"One resource to diagnose. Omit to sweep a namespace."`
	Namespace     string     `json:"namespace,omitempty" jsonschema:"Namespace to sweep when there is no target; defaults to the current namespace."`
	AllNamespaces bool       `json:"allNamespaces,omitempty" jsonschema:"Sweep every namespace when there is no target."`
	Since         string     `json:"since,omitempty" jsonschema:"Ignore what finished longer ago than this — 30m, 24h, 7d. Ongoing problems are always reported. Defaults to kx's diag_max_age setting."`
	Full          bool       `json:"full,omitempty" jsonschema:"Include healthy resources in a sweep's results."`
}

type diagnoseOutput struct {
	Context   string             `json:"context"`
	Diagnosis diagnosticDocument `json:"diagnosis"`
}

// discardListing is the Save a listing tool is built with when
// --write-listings is off (see mcpDeps.listingSave). TriageCommand and the
// rest save what they listed so the terminal can spend the numbers they
// printed; the server printed none, and saving would move the user's own
// indexes.
func discardListing(state.State) error { return nil }

func (d mcpDeps) diagnose(ctx context.Context, _ *mcp.CallToolRequest, in diagnoseInput) (*mcp.CallToolResult, diagnoseOutput, error) {
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, diagnoseOutput{}, err
	}
	if in.Target != nil && (in.Namespace != "" || in.AllNamespaces || in.Full) {
		return nil, diagnoseOutput{}, errors.New(
			"'namespace', 'allNamespaces' and 'full' apply to a sweep — a target already names its namespace. Drop them, or drop the target to sweep.")
	}
	window, err := resolveWindowAs("since", in.Since, d.Config.DiagMaxAge)
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	// The context before the client: read after, a switch in between would
	// label this cluster's answer with the next one's name — and, with
	// --write-listings, stamp its saved sweep with it (see listingSave).
	out := diagnoseOutput{Context: d.Kubectl.CurrentContext()}
	client, err := d.Kubernetes()
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	service := diagnostics.New(client)
	service.MaxAge = window

	if in.Target != nil {
		target, err := d.resolveTarget(*in.Target)
		if err != nil {
			return nil, diagnoseOutput{}, err
		}
		report, err := DiagnosticCommand{Diagnostics: service}.ExecuteResource(
			ctx, target.Kind, target.Name, target.Namespace)
		if err != nil {
			return nil, diagnoseOutput{}, err
		}
		out.Diagnosis = diagnosticDocumentOf(report, state.Ref{Mark: target.Mark})
		return nil, out, nil
	}

	namespace := in.Namespace
	if namespace == "" && !in.AllNamespaces {
		namespace = d.Kubectl.CurrentNamespace()
	}
	// Saved whole, healthy rows included, as kx diag saves it: the filter
	// below narrows only what is returned, so each index it keeps is still
	// the row's position in the saved sweep.
	result, err := TriageCommand{Diagnostics: service, Save: d.listingSave(out.Context), Window: window}.
		Execute(ctx, namespace, in.AllNamespaces, true)
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	out.Diagnosis = triageDocument(result, d.indexed())
	// Unlike --json, which carries every resource because nothing scrolls
	// past a machine: an agent pays for every token of a healthy row, and
	// checked/healthy already say how many there were.
	if !in.Full {
		kept := out.Diagnosis.Resources[:0]
		for _, resource := range out.Diagnosis.Resources {
			if resource.Verdict != diagnostics.OK.Token() {
				kept = append(kept, resource)
			}
		}
		out.Diagnosis.Resources = kept
	}
	return nil, out, nil
}

const (
	defaultTreeLimit = 500
	maxTreeLimit     = 2000
)

type treeInput struct {
	Target        *mcpTarget `json:"target,omitempty" jsonschema:"The resource to graph. Omit to graph a namespace."`
	Namespace     string     `json:"namespace,omitempty" jsonschema:"Namespace to graph when there is no target; defaults to the current namespace."`
	AllNamespaces bool       `json:"allNamespaces,omitempty" jsonschema:"Graph every namespace when there is no target."`
	Limit         int        `json:"limit,omitempty" jsonschema:"Most nodes to return, containers included; default 500, at most 2000. The graph is cut breadth-first, and truncated counts what was left out."`
}

type treeOutput struct {
	Context string       `json:"context"`
	Tree    treeDocument `json:"tree"`
	// Truncated is how many nodes the limit left out; absent when none were.
	Truncated int `json:"truncated,omitempty"`
}

func treeLimit(requested int) int {
	return clampLimit(requested, defaultTreeLimit, maxTreeLimit)
}

// pruneTree keeps the first limit nodes of roots in breadth-first order and
// returns how many it dropped.
//
// Breadth-first because the top of a graph is what names the next thing to
// look at: a forest cut depth-first would spend the whole budget on the first
// Deployment's containers and never mention the second Deployment. Sibling
// order is the graph walk's own, so the same cluster is always cut the same
// way.
func pruneTree(roots []jsonTreeNode, limit int) ([]jsonTreeNode, int) {
	total := countNodes(roots)
	if total <= limit {
		return roots, 0
	}
	kept := 0
	// Each entry is one node's list of children; the queue visits them level
	// by level, so every kept node's parent was kept before it.
	queue := []*[]jsonTreeNode{&roots}
	for len(queue) > 0 {
		siblings := queue[0]
		queue = queue[1:]
		room := limit - kept
		if len(*siblings) > room {
			*siblings = (*siblings)[:room]
		}
		kept += len(*siblings)
		for i := range *siblings {
			if len((*siblings)[i].Children) > 0 {
				queue = append(queue, &(*siblings)[i].Children)
			}
		}
	}
	return roots, total - kept
}

func countNodes(roots []jsonTreeNode) int {
	count := 0
	for _, root := range roots {
		count += 1 + countNodes(root.Children)
	}
	return count
}

func (d mcpDeps) tree(ctx context.Context, _ *mcp.CallToolRequest, in treeInput) (*mcp.CallToolResult, any, error) {
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, nil, err
	}
	if in.Target != nil && (in.Namespace != "" || in.AllNamespaces) {
		return nil, nil, errors.New(
			"'namespace' and 'allNamespaces' apply without a target — a target already names its namespace.")
	}
	// The context before the client, as in diagnose; it stamps the saved walk.
	out := treeOutput{Context: d.Kubectl.CurrentContext()}
	client, err := d.Kubernetes()
	if err != nil {
		return nil, nil, err
	}
	// With --write-listings off, Save is never reached (indexed is false);
	// listingSave's discardListing is belt and braces. On, the walk is saved
	// whole before pruneTree cuts what is returned, so a pruned node keeps
	// the index it was saved at and every number kept is a saved position.
	indexed := d.indexed()
	command := TreeCommand{Builder: graph.Builder{Client: client}, Save: d.listingSave(out.Context)}

	switch {
	case in.Target != nil:
		target, err := d.resolveTarget(*in.Target)
		if err != nil {
			return nil, nil, err
		}
		node, err := command.ExecuteResource(ctx, target.Kind, target.Name, target.Namespace, indexed)
		if err != nil {
			return nil, nil, err
		}
		// A Namespace target graphs that namespace itself, so it is the scope
		// rather than the subject — the same distinction the CLI's indexed
		// --json path draws for a Namespace row (tree.go).
		subject := scanSubject{Kind: target.Kind, Name: target.Name, Namespace: target.Namespace}
		if target.Kind == kinds.Namespace {
			subject = scanSubject{Namespace: target.Name}
		}
		out.Tree = treeDocumentOf(subject, []*tree.Node{node})
	case in.AllNamespaces:
		roots, resources, err := command.ExecuteAllNamespaces(ctx, indexed)
		if err != nil {
			return nil, nil, err
		}
		// ExecuteAllNamespaces saves nothing itself; the CLI saves the forest
		// after the walk, with no entry namespace, and so does this.
		if err := command.save(resources, "", indexed, true); err != nil {
			return nil, nil, err
		}
		out.Tree = treeDocumentOf(scanSubject{AllNamespaces: true}, roots)
	default:
		namespace := in.Namespace
		if namespace == "" {
			namespace = d.Kubectl.CurrentNamespace()
		}
		node, err := command.ExecuteNamespace(ctx, namespace, indexed)
		if err != nil {
			return nil, nil, err
		}
		out.Tree = treeDocumentOf(scanSubject{Namespace: namespace}, []*tree.Node{node})
	}
	out.Tree.Roots, out.Truncated = pruneTree(out.Tree.Roots, treeLimit(in.Limit))
	return nil, out, nil
}
