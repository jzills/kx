package render

import (
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/theme"
)

// Node is a node in a rendered tree. Built by the graph package, drawn here, so
// ownership-walking logic stays free of presentation.
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

// Tree guides, matching what Rich draws.
const (
	guideBranch = "├── "
	guideLast   = "└── "
	guideBar    = "│   "
	guideBlank  = "    "
)

// Tree draws a node and its descendants.
func (r *Renderer) Tree(root *Node) {
	var out strings.Builder
	out.WriteString(r.nodeLabel(root))
	out.WriteString("\n")
	r.writeChildren(&out, root, "")
	r.write(out.String())
}

func (r *Renderer) nodeLabel(node *Node) string {
	label := r.style(node.Style, node.Label)
	if node.Index > 0 {
		return r.style(theme.Muted, strconv.Itoa(node.Index)) + " " + label
	}
	return label
}

func (r *Renderer) writeChildren(out *strings.Builder, parent *Node, prefix string) {
	for i, child := range parent.Children {
		last := i == len(parent.Children)-1
		guide, continuation := guideBranch, guideBar
		if last {
			guide, continuation = guideLast, guideBlank
		}
		out.WriteString(prefix + guide + r.nodeLabel(child) + "\n")
		r.writeChildren(out, child, prefix+continuation)
	}
}

// Tree draws through the package-level renderer.
func Tree(root *Node) { current.Tree(root) }
