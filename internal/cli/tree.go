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
	if err := c.save(resources, namespace, indexed, false); err != nil {
		return nil, err
	}
	return node, nil
}

// ExecuteNamespace graphs the whole ownership forest for a namespace.
func (c TreeCommand) ExecuteNamespace(ctx context.Context, namespace string, indexed bool) (*tree.Node, error) {
	node, resources, err := c.Builder.BuildNamespace(ctx, namespace, indexed, 0)
	if err != nil {
		return nil, err
	}
	if err := c.save(resources, namespace, indexed, false); err != nil {
		return nil, err
	}
	return node, nil
}

// ExecuteAllNamespaces graphs the ownership forest for every namespace, one
// root per namespace, and returns the walked resources in index order.
//
// Numbering runs continuously through the forest rather than restarting in each
// namespace: two namespaces would otherwise both hold a node numbered 1, and an
// index that names two rows names neither. Each resource records the namespace
// it came from, which is what lets the saved indexes resolve afterwards.
func (c TreeCommand) ExecuteAllNamespaces(
	ctx context.Context, indexed bool,
) ([]*tree.Node, []graph.Resource, error) {
	namespaces, err := c.Builder.Namespaces(ctx)
	if err != nil {
		return nil, nil, err
	}
	roots := make([]*tree.Node, 0, len(namespaces))
	var resources []graph.Resource
	for _, namespace := range namespaces {
		node, walked, err := c.Builder.BuildNamespace(ctx, namespace, indexed, len(resources))
		if err != nil {
			return nil, nil, err
		}
		roots = append(roots, node)
		resources = append(resources, walked...)
	}
	return roots, resources, nil
}

// save records the tree's nodes as a state entry.
//
// A tree entry carries no Query: it wasn't produced by `kx get`, so there is
// nothing to re-run if it goes stale.
//
// allNamespaces is passed rather than inferred from an empty namespace: a walk
// records the namespace on every resource it returns, including a
// single-namespace one, so nothing about the resources distinguishes the two
// scopes afterwards.
func (c TreeCommand) save(
	resources []graph.Resource, namespace string, indexed, allNamespaces bool,
) error {
	if !indexed || len(resources) == 0 {
		return nil
	}
	// Order is the order the indexes were assigned during the walk.
	entries := make([]state.Resource, 0, len(resources))
	for _, resource := range resources {
		entries = append(entries, state.Resource{
			Name: resource.Name, Kind: resource.Kind, Namespace: resource.Namespace,
		})
	}
	return c.Save(state.State{
		Resources:     state.NewOrderedResources(entries),
		Namespace:     namespace,
		AllNamespaces: allNamespaces,
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

// treeInvocation renders the command line the page says it came from.
//
// One helper for all three of tree's pages — the -A forest, a namespace sweep,
// and a single indexed resource — because they had drifted: the -A branch left
// indexFlag out, so `kx tree -A --no-index --html` published a page claiming it
// was produced by `kx tree -A`, and re-running that prints a numbered tree the
// page does not have.
func treeInvocation(scope string, indexed bool, port int) string {
	return invocation("tree", scope, indexFlag(indexed), portFlag(port))
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
		Use:        "tree [index]",
		SuggestFor: []string{"graph", "owners", "children"},
		Short:      "Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace.",
		Long:       "Graphs ownership references from controllers down to containers. With no index, graphs every workload in the current namespace, or in the namespace given by -n, or every namespace as a forest with -A. A Namespace index graphs that namespace. Assigns indexes to tree nodes by default; --no-index skips that.",
		Example:    "  kx tree\n  kx tree 1\n  kx tree --no-index\n  kx tree -n prod\n  kx tree -A",
		Args:       cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			noIndex, _ := cmd.Flags().GetBool("no-index")
			indexed := !noIndex

			html, _ := cmd.Flags().GetBool("html")
			port, _ := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")
			out, _ := cmd.Flags().GetString("out")
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen, Out: out}
			if err := htmlOpts.validate(
				cmd.Flags().Changed("port"), cmd.Flags().Changed("no-open")); err != nil {
				return err
			}

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
					roots, resources, err := command.ExecuteAllNamespaces(ctx, indexed)
					stop()
					if err != nil {
						return err
					}
					// Saved with no entry namespace: the forest spans them, and
					// each resource records its own.
					if err := command.save(resources, "", indexed, true); err != nil {
						return err
					}
					render.ScopeBanner("Namespace", render.AllNamespaces, "")
					for i, root := range roots {
						if i > 0 {
							render.Blank()
						}
						render.Tree(root)
					}
					if !htmlOpts.Enabled {
						return nil
					}
					meta, err := pageMeta(services.Config.Theme, "tree · "+render.AllNamespaces,
						treeInvocation(scopeArgs("", true), indexed, port))
					if err != nil {
						return err
					}
					page, err := web.RenderTree(web.TreePage{
						Meta: meta, Scope: scopeCaption("Namespace", render.AllNamespaces),
						AllNamespaces: true, Roots: roots,
					})
					if err != nil {
						return err
					}
					return deliverPage(ctx, page, htmlOpts)
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
				meta, err := pageMeta(services.Config.Theme, "tree · "+namespace,
					treeInvocation(scopeArgs(namespace, false), indexed, port))
				if err != nil {
					return err
				}
				page, err := web.RenderTree(web.TreePage{
					Meta: meta, Scope: scopeCaption("Namespace", namespace), Root: node,
				})
				if err != nil {
					return err
				}
				return deliverPage(ctx, page, htmlOpts)
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
			meta, err := pageMeta(services.Config.Theme, "tree · "+node.Label,
				treeInvocation(args[0], indexed, port))
			if err != nil {
				return err
			}
			page, err := web.RenderTree(web.TreePage{Meta: meta, Scope: scope, Root: node})
			if err != nil {
				return err
			}
			return deliverPage(ctx, page, htmlOpts)
		},
	}
	cmd.Flags().Bool("no-index", false,
		"Skip assigning indexes to tree nodes and don't update state")
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to sweep; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"Sweep every namespace, as a forest of per-namespace trees; nodes are indexed continuously across it")
	cmd.Flags().Bool("html", false,
		"Render the tree as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	cmd.Flags().String("out", "",
		"Write the HTML report to this file instead of serving it in a browser")
	return cmd
}
