package render

import (
	"strconv"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/theme"
)

// TriageResult is a namespace sweep, ready to render.
type TriageResult struct {
	Namespace string
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
	if result.Checked == 0 {
		r.Caption("Mixed", result.Namespace, "0 checked")
		return
	}
	if len(result.Reports) == 0 {
		r.line(r.style(theme.Muted, "Mixed · "+result.Namespace+" · ") +
			r.style(theme.Success, strconv.Itoa(result.Checked)+" checked · all healthy"))
		return
	}

	r.Caption("Mixed", result.Namespace, strconv.Itoa(result.Checked)+" checked")
	r.Blank()

	columns := []Column{
		{Header: "", Right: true}, {Header: "KIND"}, {Header: "NAME"},
		{Header: "VERDICT"}, {Header: "TOP FINDING"},
	}
	rows := make([][]Cell, 0, len(result.Reports))
	for position, report := range result.Reports {
		top := ""
		if len(report.Findings) > 0 {
			// One finding per line keeps the table scannable; the rest is one
			// `kx diag <index>` away.
			top = report.Findings[0].Summary
		}
		rows = append(rows, []Cell{
			Styled(strconv.Itoa(position+1), theme.Muted),
			Plain(string(report.Kind)),
			Plain(report.Name),
			Styled(report.Verdict.String(), severityStyle(report.Verdict)),
			Styled(top, theme.Body),
		})
	}
	r.Table(columns, rows)

	r.Blank()
	label := "resources"
	if result.Healthy == 1 {
		label = "resource"
	}
	r.line(r.style(theme.Muted, strconv.Itoa(result.Healthy)+" healthy "+label+
		" not shown · kx diag <index> for detail"))
	for _, name := range result.Dropped {
		r.line(r.style(theme.Muted,
			name+" shares a name with an indexed resource and was omitted"))
	}
}

// Triage renders through the package-level renderer.
func Triage(result TriageResult) { current.Triage(result) }
