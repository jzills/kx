package cli

import (
	"context"
	"strings"

	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/tree"
	"github.com/jzills/kx/internal/web"
	"github.com/spf13/cobra"
)

// TreeCommand graphs ownership references, for one resource or a whole
// namespace.
type TreeCommand struct {
	Builder graph.Builder
	State   IndexResolver
	// Save records an indexed tree as the current listing, so the numbers
	// shown can be used by later commands.
	Save func(state.State) error
}

// Execute graphs the resource an index names. A Namespace row graphs that
// namespace itself — its own name, not the namespace the `kx get ns` ran in.
func (c TreeCommand) Execute(ctx context.Context, index int, indexed bool) (*tree.Node, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return nil, err
	}
	if kind == kinds.Namespace {
		return c.ExecuteNamespace(ctx, name, indexed)
	}

	node, resources, err := c.Builder.BuildResource(ctx, kind, name, namespace, indexed)
	if err != nil {
		return nil, err
	}
	if err := c.save(resources, namespace, indexed); err != nil {
		return nil, err
	}
	return node, nil
}

// ExecuteNamespace graphs the whole ownership forest for a namespace.
func (c TreeCommand) ExecuteNamespace(ctx context.Context, namespace string, indexed bool) (*tree.Node, error) {
	node, resources, err := c.Builder.BuildNamespace(ctx, namespace, indexed)
	if err != nil {
		return nil, err
	}
	if err := c.save(resources, namespace, indexed); err != nil {
		return nil, err
	}
	return node, nil
}

// save records the tree's nodes as a state entry.
//
// A tree entry carries no Query: it wasn't produced by `kx get`, so there is
// nothing to re-run if it goes stale.
func (c TreeCommand) save(resources []graph.Resource, namespace string, indexed bool) error {
	if !indexed || len(resources) == 0 {
		return nil
	}
	// Order is the order the indexes were assigned during the walk.
	entries := make([]state.Resource, 0, len(resources))
	for _, resource := range resources {
		entries = append(entries, state.Resource{Name: resource.Name, Kind: resource.Kind})
	}
	return c.Save(state.State{
		Resources: state.NewOrderedResources(entries),
		Namespace: namespace,
	})
}

// indexFlag renders --index for the invocation line when node indexes were
// assigned, so the page's provenance line matches what was actually run.
func indexFlag(indexed bool) string {
	if indexed {
		return "--index"
	}
	return ""
}

// scopeCaption joins non-empty parts with " · " for the page's muted caption
// line, matching the text render.Banner/render.ScopeBanner already printed
// to the terminal just above render.Tree, so the two must not read
// differently.
func scopeCaption(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " · ")
}

func newTreeCommand(services Services) *cobra.Command {
	var indexed bool
	cmd := &cobra.Command{
		Use:   "tree [index]",
		Short: "Show the ownership graph for an indexed resource, or the whole current namespace when no index is given; --index assigns indexes to tree nodes. A Namespace index graphs that namespace.",
		Long: "Graphs ownership references from controllers down to containers.\n" +
			"With no index, graphs every workload in the current namespace.\n" +
			"A Namespace index graphs that namespace.",
		Example: "  kx tree\n  kx tree 1\n  kx tree 1 --index",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			html, _ := cmd.Flags().GetBool("html")
			port, _ := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen}

			client, err := services.Kubernetes()
			if err != nil {
				return err
			}
			command := TreeCommand{
				Builder: graph.Builder{Client: client},
				State:   services.State,
				Save:    services.State.Save,
			}
			ctx := cmd.Context()

			if len(args) == 0 {
				namespace := services.Kubectl.CurrentNamespace()
				render.ScopeBanner("Namespace", namespace, "")
				stop := render.Status("resolving ownership graph")
				node, err := command.ExecuteNamespace(ctx, namespace, indexed)
				stop()
				if err != nil {
					return err
				}
				render.Tree(node)
				if !htmlOpts.Enabled {
					return nil
				}
				meta, err := pageMeta(services.Config.Theme, "kx tree · "+namespace,
					invocation("tree", indexFlag(indexed), portFlag(port)))
				if err != nil {
					return err
				}
				page, err := web.RenderTree(web.TreePage{
					Meta: meta, Scope: scopeCaption("Namespace", namespace), Root: node,
				})
				if err != nil {
					return err
				}
				return servePage(ctx, page, htmlOpts)
			}

			index, err := parseIndex("index", args[0])
			if err != nil {
				return err
			}
			name, namespace, kind, err := services.State.Fields(index)
			if err != nil {
				return err
			}
			var scope string
			if kind == kinds.Namespace {
				scope = scopeCaption("Namespace", name)
				render.ScopeBanner("Namespace", name, "")
			} else {
				scope = scopeCaption(string(kind)+"/"+name, namespace)
				render.Banner(string(kind), name, namespace, "")
			}
			stop := render.Status("resolving ownership graph")
			node, err := command.Execute(ctx, index, indexed)
			stop()
			if err != nil {
				return err
			}
			render.Tree(node)
			if !htmlOpts.Enabled {
				return nil
			}
			// node.Label is already "Kind/Name" or "Namespace/name" (graph.go
			// builds the root that way), so the page title reuses it rather
			// than re-deriving kind/name separately.
			meta, err := pageMeta(services.Config.Theme, "kx tree · "+node.Label,
				invocation("tree", args[0], indexFlag(indexed), portFlag(port)))
			if err != nil {
				return err
			}
			page, err := web.RenderTree(web.TreePage{Meta: meta, Scope: scope, Root: node})
			if err != nil {
				return err
			}
			return servePage(ctx, page, htmlOpts)
		},
	}
	cmd.Flags().BoolVarP(&indexed, "index", "i", false,
		"Assign indexes to tree nodes and update state")
	cmd.Flags().Bool("html", false,
		"Render the tree as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	return cmd
}
