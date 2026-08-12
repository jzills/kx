package render

import (
	"strconv"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/theme"
)

// TriageResult is a namespace sweep, ready to render.
type TriageResult struct {
	Namespace string
	// AllNamespaces adds a namespace column beside the index one. A
	// cluster-wide sweep is indexed like any other; the namespace is what
	// separates two rows whose names are unique only within their own.
	AllNamespaces bool
	// Checked is the number of resources swept, most of which are healthy and
	// never appear as a row unless Full is set.
	Checked int
	// Reports are the terminal table's rows, most severe first: unhealthy
	// resources only, or every swept resource when Full is set.
	Reports []diagnostics.Report
	// All is every swept resource, most severe first, regardless of Full — the
	// HTML report shows the full inventory unconditionally, since its grid can
	// filter healthy rows away client-side rather than needing them dropped
	// before they ever reach the page.
	All     []diagnostics.Report
	Healthy int
	// Full mirrors kx diag --full: Reports already includes healthy resources,
	// so the footer must not also claim some were left out.
	Full bool
}

// Triage renders a namespace sweep: one row per unhealthy resource, indexed to
// match the saved state, with healthy resources collapsed into the footer —
// or one row per every swept resource, healthy included, when Full is set.
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
	// A cluster-wide sweep carries NAMESPACE as well, not instead: the index is
	// what `kx diag <n>` acts on, and the namespace is what tells two rows
	// called web-abc apart. It used to swap one for the other, back when an -A
	// sweep had no indexes to print.
	columns := []Column{{Header: "X", Right: true}, {Header: "KIND"}}
	if result.AllNamespaces {
		columns = append(columns, Column{Header: "NAMESPACE"})
	}
	columns = append(columns,
		Column{Header: "NAME"}, Column{Header: "VERDICT"},
		Column{Header: "TOP FINDING", Flex: true})
	nameBudget := triageNameBudget(r.width(), result)

	rows := make([][]Cell, 0, len(result.Reports))
	for position, report := range result.Reports {
		top := ""
		if len(report.Findings) > 0 {
			// One finding per line keeps the table scannable; the rest is one
			// `kx diag <index>` away.
			top = report.Findings[0].Summary
		}
		// The shapes share their last three cells; a cluster-wide sweep adds
		// the namespace between the kind and the name.
		lead := []Cell{
			Styled(strconv.Itoa(position+1), theme.Muted),
			Plain(string(report.Kind)),
		}
		if result.AllNamespaces {
			lead = append(lead, Styled(report.Namespace, theme.Muted))
		}
		rows = append(rows, append(lead,
			Plain(ellipsize(report.Name, nameBudget)),
			Styled(report.Verdict.String(), severityStyle(report.Verdict)),
			Styled(top, theme.Body)))
	}
	r.Table(columns, rows)

	r.Blank()
	hint := "kx diag <index> for detail"
	footer := hint
	if !result.Full {
		label := "resources"
		if result.Healthy == 1 {
			label = "resource"
		}
		footer = strconv.Itoa(result.Healthy) + " healthy " + label + " not shown · " + hint
	}
	r.line(r.style(theme.Muted, footer))
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
