package graph

import (
	"sort"
	"strings"

	"github.com/jzills/kx/internal/kinds"
)

// sortRoots orders the forest's roots by kind, then by name, so the same
// namespace always renders in the same order.
func sortRoots(roots []ownerRef, order map[kinds.Kind]int) {
	sort.SliceStable(roots, func(i, j int) bool {
		left, right := order[roots[i].kind], order[roots[j].kind]
		if left != right {
			return left < right
		}
		return roots[i].object.GetName() < roots[j].object.GetName()
	})
}

// formatSelector renders a label map as a kubectl-style selector, sorted so the
// query is stable run to run.
func formatSelector(labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for key, value := range labels {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}
