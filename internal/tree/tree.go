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
	Index    int
	Children []*Node
}

// Add appends a child and returns it, so builders can descend as they walk.
func (n *Node) Add(label, style string) *Node {
	child := &Node{Label: label, Style: style}
	n.Children = append(n.Children, child)
	return child
}

// AddIndexed appends a child carrying a 1-based index.
func (n *Node) AddIndexed(label, style string, index int) *Node {
	child := &Node{Label: label, Style: style, Index: index}
	n.Children = append(n.Children, child)
	return child
}
