package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/state"
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
}

// readOnlyTool annotates a tool that reads the cluster and writes nothing.
func readOnlyTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
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
	if in.Namespace != "" && in.AllNamespaces {
		return nil, listOutput{}, errors.New("'allNamespaces' and 'namespace' cannot be combined.")
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
