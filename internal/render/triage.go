package render

import (
	"strconv"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/theme"
)

// AllNamespacesNote explains why an -A listing has no indexes, since the
// absence is otherwise indistinguishable from a bug.
const AllNamespacesNote = "indexes not saved for all-namespace listings — " +
	"scope to a namespace (-n or kx ns) to select"

// TriageResult is a namespace sweep, ready to render.
type TriageResult struct {
	Namespace string
	// AllNamespaces swaps the index column for a namespace one. A cluster-wide
	// sweep saves no state, because names are unique only within a namespace,
	// so an X column here would print numbers that resolve to nothing.
	AllNamespaces bool
	// Checked is the number of resources swept, most of which are healthy and
	// never appear as a row.
	Checked int
	// Reports are the unhealthy resources only, most severe first.
	Reports []diagnostics.Report
	Healthy int
	// Dropped names rows lost to a cross-kind name collision.
	Dropped []string
}

// Triage renders a namespace sweep: one row per unhealthy resource, indexed to
// match the saved state, with healthy resources collapsed into the footer.
//
// The caption follows the same "{kind} · {namespace} · {count} {noun}" shape as
// kx get and kx state, with "Mixed" as the kind since a sweep spans whatever
// kinds exist. It counts "checked" rather than "items" because the number is
// resources examined, not rows in the table below it.
func (r *Renderer) Triage(result TriageResult) {
	scope := result.Namespace
	if result.AllNamespaces {
		// The same words kx get -A captions itself with.
		scope = "all namespaces"
	}
	if result.Checked == 0 {
		r.Caption("Mixed", scope, "0 checked")
		return
	}
	if len(result.Reports) == 0 {
		r.line(r.style(theme.Muted, "Mixed · "+scope+" · ") +
			r.style(theme.Success, strconv.Itoa(result.Checked)+" checked · all healthy"))
		return
	}

	// No blank line between the caption and the table: every other indexed
	// listing puts its header row directly under the caption, and the Python
	// renderer's extra line here was alone in doing otherwise.
	r.Caption("Mixed", scope, strconv.Itoa(result.Checked)+" checked")

	// Headed "X" like every other indexed listing. The Python renderer left it
	// blank, alone among the indexed tables; these numbers are indexes passed
	// to `kx diag <index>` exactly as `kx get`'s are, so they are labelled the
	// same way.
	//
	// A cluster-wide sweep has no indexes to print, so NAMESPACE takes the
	// column instead — without it two rows called web-abc are indistinguishable.
	var columns []Column
	if result.AllNamespaces {
		columns = []Column{
			{Header: "KIND"}, {Header: "NAMESPACE"}, {Header: "NAME"},
			{Header: "VERDICT"}, {Header: "TOP FINDING", Flex: true},
		}
	} else {
		columns = []Column{
			{Header: "X", Right: true}, {Header: "KIND"}, {Header: "NAME"},
			{Header: "VERDICT"}, {Header: "TOP FINDING", Flex: true},
		}
	}
	nameBudget := triageNameBudget(r.width(), result)

	rows := make([][]Cell, 0, len(result.Reports))
	for position, report := range result.Reports {
		top := ""
		if len(report.Findings) > 0 {
			// One finding per line keeps the table scannable; the rest is one
			// `kx diag <index>` away.
			top = report.Findings[0].Summary
		}
		// The shapes share their last three cells and differ only in what
		// leads: an index and a kind, or a kind and the namespace standing in
		// for the index that a cluster-wide sweep has none of.
		var lead []Cell
		if result.AllNamespaces {
			lead = []Cell{
				Plain(string(report.Kind)),
				Styled(report.Namespace, theme.Muted),
			}
		} else {
			lead = []Cell{
				Styled(strconv.Itoa(position+1), theme.Muted),
				Plain(string(report.Kind)),
			}
		}
		rows = append(rows, append(lead,
			Plain(ellipsize(report.Name, nameBudget)),
			Styled(report.Verdict.String(), severityStyle(report.Verdict)),
			Styled(top, theme.Body)))
	}
	r.Table(columns, rows)

	r.Blank()
	label := "resources"
	if result.Healthy == 1 {
		label = "resource"
	}
	footer := strconv.Itoa(result.Healthy) + " healthy " + label + " not shown"
	if result.AllNamespaces {
		footer += " · " + AllNamespacesNote
	} else {
		footer += " · kx diag <index> for detail"
	}
	r.line(r.style(theme.Muted, footer))
	for _, name := range result.Dropped {
		r.line(r.style(theme.Muted,
			name+" shares a name with an indexed resource and was omitted"))
	}
}

// What the NAME column can never have: two-space padding on both sides of all
// five columns, the widest KIND the sweep emits, and the widest VERDICT.
const (
	triageKindWidth    = 21 // PersistentVolumeClaim
	triageVerdictWidth = 8  // critical, warnings
	triageIndexWidth   = 2  // room for a two-digit index
	triageFixedWidth   = 5*2*len(cellPad) + triageKindWidth + triageVerdictWidth
)

// minNameWidth is the point below which a name carries nothing.
const minNameWidth = 8

// triageNameBudget is the room the NAME column gets.
//
// Names are truncated against their own budget rather than left to the flex
// column: with everything else fixed, a very long name would otherwise squeeze
// TOP FINDING out entirely.
//
// The first column is an index or a namespace depending on the shape. NAMESPACE
// replaces X rather than joining it, so only one of the two is ever charged to
// the name.
func triageNameBudget(width int, result TriageResult) int {
	scope := triageIndexWidth
	if result.AllNamespaces {
		scope = widestNamespace(result.Reports)
	}
	budget := width - triageFixedWidth - scope
	if budget < minNameWidth {
		budget = minNameWidth
	}
	return budget
}

// widestNamespace is how much room the -A shape's NAMESPACE column needs.
func widestNamespace(reports []diagnostics.Report) int {
	widest := width("NAMESPACE")
	for _, report := range reports {
		if n := width(report.Namespace); n > widest {
			widest = n
		}
	}
	return widest
}

// Triage renders through the package-level renderer.
func Triage(result TriageResult) { current.Triage(result) }
