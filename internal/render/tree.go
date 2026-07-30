package render

import (
	"strconv"
	"strings"

	"github.com/jzills/kx/internal/theme"
	"github.com/jzills/kx/internal/tree"
)

// Tree guides, matching what Rich draws.
const (
	guideBranch = "├── "
	guideLast   = "└── "
	guideBar    = "│   "
	guideBlank  = "    "
)

// Tree draws a node and its descendants.
func (r *Renderer) Tree(root *tree.Node) {
	var out strings.Builder
	out.WriteString(r.nodeLabel(root))
	out.WriteString("\n")
	r.writeChildren(&out, root, "")
	r.write(out.String())
}

func (r *Renderer) nodeLabel(node *tree.Node) string {
	label := r.style(node.Style, node.Label)
	if node.Index > 0 {
		return r.style(theme.Muted, strconv.Itoa(node.Index)) + " " + label
	}
	return label
}

func (r *Renderer) writeChildren(out *strings.Builder, parent *tree.Node, prefix string) {
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
func Tree(root *tree.Node) { current.Tree(root) }
