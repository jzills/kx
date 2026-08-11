package cli

import (
	"context"
	"errors"
	"fmt"
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

// ExecuteAllNamespaces graphs the ownership forest for every namespace, one
// root per namespace.
//
// Results are never indexed or saved, matching kx get -A and kx diag -A:
// names repeat across namespaces, so there is nothing stable to index.
func (c TreeCommand) ExecuteAllNamespaces(ctx context.Context) ([]*tree.Node, error) {
	namespaces, err := c.Builder.Namespaces(ctx)
	if err != nil {
		return nil, err
	}
	roots := make([]*tree.Node, 0, len(namespaces))
	for _, namespace := range namespaces {
		node, _, err := c.Builder.BuildNamespace(ctx, namespace, false)
		if err != nil {
			return nil, err
		}
		roots = append(roots, node)
	}
	return roots, nil
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

// indexFlag renders --no-index for the invocation line when node indexes
// were skipped, so the page's provenance line matches what was actually run.
// Indexing is the default, so the common case renders nothing.
func indexFlag(indexed bool) string {
	if !indexed {
		return "--no-index"
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
	cmd := &cobra.Command{
		Use:     "tree [index]",
		Short:   "Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace.",
		Long:    "Graphs ownership references from controllers down to containers. With no index, graphs every workload in the current namespace, or in the namespace given by -n, or every namespace as a forest with -A. A Namespace index graphs that namespace. Assigns indexes to tree nodes by default; --no-index skips that.",
		Example: "  kx tree\n  kx tree 1\n  kx tree --no-index\n  kx tree -n prod\n  kx tree -A",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			noIndex, _ := cmd.Flags().GetBool("no-index")
			indexed := !noIndex

			html, _ := cmd.Flags().GetBool("html")
			port, _ := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen}

			namespaceFlag, _ := cmd.Flags().GetString("namespace")
			allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")

			if cmd.Flags().Changed("namespace") && allNamespaces {
				return errors.New(
					"'--all-namespaces' and '--namespace' cannot be combined.")
			}
			// An index already carries the namespace it was listed from, so a
			// scope flag next to one is a contradiction rather than a refinement.
			scopeFlag := ""
			if cmd.Flags().Changed("namespace") {
				scopeFlag = "--namespace"
			}
			if allNamespaces {
				scopeFlag = "--all-namespaces"
			}
			if len(args) > 0 && scopeFlag != "" {
				return fmt.Errorf(
					"'%s' cannot be combined with an index — an index already "+
						"carries the namespace it was listed from. Drop the flag, "+
						"or drop the index to sweep the namespace instead.", scopeFlag)
			}

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
				if allNamespaces {
					stop := render.Status("resolving ownership graphs")
					roots, err := command.ExecuteAllNamespaces(ctx)
					stop()
					if err != nil {
						return err
					}
					render.ScopeBanner("Namespace", "all namespaces", "")
					for i, root := range roots {
						if i > 0 {
							render.Blank()
						}
						render.Tree(root)
					}
					if !htmlOpts.Enabled {
						return nil
					}
					meta, err := pageMeta(services.Config.Theme, "kx tree · all namespaces",
						invocation("tree", scopeArgs("", true), portFlag(port)))
					if err != nil {
						return err
					}
					page, err := web.RenderTree(web.TreePage{
						Meta: meta, Scope: scopeCaption("Namespace", "all namespaces"),
						AllNamespaces: true, Roots: roots,
					})
					if err != nil {
						return err
					}
					return servePage(ctx, page, htmlOpts)
				}

				namespace := namespaceFlag
				if namespace == "" {
					namespace = services.Kubectl.CurrentNamespace()
				}
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
					invocation("tree", scopeArgs(namespace, false), indexFlag(indexed), portFlag(port)))
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
	cmd.Flags().Bool("no-index", false,
		"Skip assigning indexes to tree nodes and don't update state")
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to sweep; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"Sweep every namespace, as a forest of per-namespace trees; results are not indexed")
	cmd.Flags().Bool("html", false,
		"Render the tree as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	return cmd
}
