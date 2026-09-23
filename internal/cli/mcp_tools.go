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
// public surface can be read in one place.
func registerMCPTools(server *mcp.Server, deps mcpDeps) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_marks",
		Description: "List the kx marks: names the user pinned to resources, usable as a target's mark.",
		Annotations: readOnlyTool("List marks"),
	}, deps.listMarks)
	mcp.AddTool(server, &mcp.Tool{
		Name: "mark",
		Description: "Pin a name to a resource so the user can reach it as @name in kx, e.g. to hand " +
			"back the resource you found at fault. Refuses a name that is already a mark. Writes kx's " +
			"local state only, never the cluster.",
		Annotations: &mcp.ToolAnnotations{Title: "Mark a resource", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, deps.mark)
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_resources",
		Description: "List resources of one kind by name and namespace — the names the other tools take. " +
			"Defaults to the current namespace.",
		Annotations: readOnlyTool("List resources"),
	}, deps.listResources)
	mcp.AddTool(server, &mcp.Tool{
		Name: "diagnose",
		Description: "Diagnose Kubernetes health. With a target, analyses that resource — replica counts, " +
			"container states, restarts, warning events — and returns findings ranked most specific first. " +
			"With no target, sweeps a namespace (or every namespace) and returns the unhealthy resources. " +
			"Supports Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, " +
			"Ingress, Pod and Node.",
		Annotations: readOnlyTool("Diagnose"),
	}, deps.diagnose)
	// Registered with an untyped output: a tree node's children are tree
	// nodes, and the SDK's schema inference refuses a recursive type.
	mcp.AddTool[treeInput, any](server, &mcp.Tool{
		Name: "tree",
		Description: "Show ownership: what a resource owns and is owned by (Deployment → ReplicaSet → Pod → " +
			"containers), or the whole ownership forest of a namespace when there is no target.",
		Annotations: readOnlyTool("Ownership tree"),
	}, deps.tree)
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

func boolPtr(value bool) *bool { return &value }

// mcpMark is one mark as a tool reports it. Resource, not Name, holds the
// resource's name: Name is the mark's own.
type mcpMark struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Context   string `json:"context,omitempty"`
}

func mcpMarkOf(name string, mark state.Mark) mcpMark {
	return mcpMark{
		Name: name, Kind: string(mark.Kind), Resource: mark.Name,
		Namespace: mark.Namespace, Context: mark.Context,
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
	name := strings.TrimPrefix(in.Name, "@")
	if err := validMarkName(name); err != nil {
		return nil, markOutput{}, err
	}
	marks, err := d.State.Marks()
	if err != nil {
		return nil, markOutput{}, err
	}
	if existing, ok := marks[name]; ok {
		return nil, markOutput{}, fmt.Errorf(
			"@%s already marks %s/%s — marks belong to the user, so this tool never moves one. Choose another name.",
			name, existing.Kind, existing.Name)
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
		Context:  d.Kubectl.CurrentContext(),
	}
	if err := d.State.SaveMark(name, mark); err != nil {
		return nil, markOutput{}, err
	}
	return nil, markOutput{Context: mark.Context, Mark: mcpMarkOf(name, mark)}, nil
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
		if err := validObjectName("namespace", in.Namespace); err != nil {
			return nil, listOutput{}, err
		}
	}
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, listOutput{}, err
	}
	kind := kinds.Normalize(in.Kind)
	isClusterScoped := clusterScoped(in.Kind)
	if isClusterScoped && (in.Namespace != "" || in.AllNamespaces) {
		flag := "namespace"
		if in.AllNamespaces {
			flag = "allNamespaces"
		}
		return nil, listOutput{}, clusterScopedScopeError(flag, in.Kind)
	}

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

	limit := in.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)
	out := listOutput{
		Context: d.Kubectl.CurrentContext(), Kind: string(kind), Namespace: namespace,
		AllNamespaces: in.AllNamespaces, Total: len(table.Entries),
		Resources: make([]listedResource, 0, min(len(table.Entries), limit)),
	}
	for _, entry := range table.Entries[:min(len(table.Entries), limit)] {
		rowNamespace := namespace
		if entry.Namespace != "" {
			rowNamespace = entry.Namespace
		}
		out.Resources = append(out.Resources, listedResource{Kind: string(kind), Name: entry.Name, Namespace: rowNamespace})
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

// discardListing is the Save a server-side sweep is built with. TriageCommand
// saves what it swept so the terminal can spend the numbers it printed; the
// server printed none, and saving would move the user's own indexes.
func discardListing(state.State) error { return nil }

func (d mcpDeps) diagnose(ctx context.Context, _ *mcp.CallToolRequest, in diagnoseInput) (*mcp.CallToolResult, diagnoseOutput, error) {
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, diagnoseOutput{}, err
	}
	if in.Target != nil && (in.Namespace != "" || in.AllNamespaces || in.Full) {
		return nil, diagnoseOutput{}, errors.New(
			"'namespace', 'allNamespaces' and 'full' apply to a sweep — a target already names its namespace. Drop them, or drop the target to sweep.")
	}
	window, err := resolveWindow(in.Since, d.Config.DiagMaxAge)
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	client, err := d.Kubernetes()
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	service := diagnostics.New(client)
	service.MaxAge = window
	out := diagnoseOutput{Context: d.Kubectl.CurrentContext()}

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
	result, err := TriageCommand{Diagnostics: service, Save: discardListing, Window: window}.
		Execute(ctx, namespace, in.AllNamespaces, true)
	if err != nil {
		return nil, diagnoseOutput{}, err
	}
	out.Diagnosis = triageDocument(result, false)
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

type treeInput struct {
	Target        *mcpTarget `json:"target,omitempty" jsonschema:"The resource to graph. Omit to graph a namespace."`
	Namespace     string     `json:"namespace,omitempty" jsonschema:"Namespace to graph when there is no target; defaults to the current namespace."`
	AllNamespaces bool       `json:"allNamespaces,omitempty" jsonschema:"Graph every namespace when there is no target."`
}

type treeOutput struct {
	Context string       `json:"context"`
	Tree    treeDocument `json:"tree"`
}

func (d mcpDeps) tree(ctx context.Context, _ *mcp.CallToolRequest, in treeInput) (*mcp.CallToolResult, any, error) {
	if err := scopeConflict(in.Namespace, in.AllNamespaces); err != nil {
		return nil, nil, err
	}
	if in.Target != nil && (in.Namespace != "" || in.AllNamespaces) {
		return nil, nil, errors.New(
			"'namespace' and 'allNamespaces' apply without a target — a target already names its namespace.")
	}
	client, err := d.Kubernetes()
	if err != nil {
		return nil, nil, err
	}
	// Save is never reached with indexed=false; discardListing is belt and braces.
	command := TreeCommand{Builder: graph.Builder{Client: client}, Save: discardListing}
	out := treeOutput{Context: d.Kubectl.CurrentContext()}

	switch {
	case in.Target != nil:
		target, err := d.resolveTarget(*in.Target)
		if err != nil {
			return nil, nil, err
		}
		node, err := command.ExecuteResource(ctx, target.Kind, target.Name, target.Namespace, false)
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
		roots, _, err := command.ExecuteAllNamespaces(ctx, false)
		if err != nil {
			return nil, nil, err
		}
		out.Tree = treeDocumentOf(scanSubject{AllNamespaces: true}, roots)
	default:
		namespace := in.Namespace
		if namespace == "" {
			namespace = d.Kubectl.CurrentNamespace()
		}
		node, err := command.ExecuteNamespace(ctx, namespace, false)
		if err != nil {
			return nil, nil, err
		}
		out.Tree = treeDocumentOf(scanSubject{Namespace: namespace}, []*tree.Node{node})
	}
	return nil, out, nil
}
