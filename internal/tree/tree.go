// Package tree is the shape of a rendered ownership tree.
//
// It sits between the graph walk that builds a tree and the renderer that draws
// one, so neither has to depend on the other: graph would otherwise import the
// renderer, and the renderer already imports diagnostics, which imports graph.
package tree

// Node is a node in a tree.
type Node struct {
	// Label is the node's text, without any index prefix.
	Label string
	// Style names the semantic style the label is drawn in.
	Style string
	// Index is the 1-based number shown before the label; 0 means unindexed.
	Index int
	// Kind and Name are the resource this node stands for, carried beside the
	// Label rather than encoded in it.
	//
	// Label is display text — "rs/web-7d8f", "container: app" — and --json must
	// not make a consumer parse one back apart to recover two fields the graph
	// walk already had. Kind is empty for a node that is not a Kubernetes
	// resource: a pod's container leaf, which Name still carries.
	Kind     string
	Name     string
	Children []*Node
}

// Add appends a child and returns it, so builders can descend as they walk.
func (n *Node) Add(label, style string) *Node {
	child := &Node{Label: label, Style: style}
	n.Children = append(n.Children, child)
	return child
}

// AddResource appends a child that stands for a Kubernetes resource, carrying
// the kind and name beside the label the renderer draws.
func (n *Node) AddResource(label, style, kind, name string, index int) *Node {
	child := &Node{Label: label, Style: style, Kind: kind, Name: name, Index: index}
	n.Children = append(n.Children, child)
	return child
}

// AddContainer appends a pod's container leaf: named, but not a resource of
// its own, so it carries no kind.
func (n *Node) AddContainer(label, style, name string) *Node {
	child := &Node{Label: label, Style: style, Name: name}
	n.Children = append(n.Children, child)
	return child
}

// AddIndexed appends a child carrying a 1-based index.
func (n *Node) AddIndexed(label, style string, index int) *Node {
	child := &Node{Label: label, Style: style, Index: index}
	n.Children = append(n.Children, child)
	return child
}
