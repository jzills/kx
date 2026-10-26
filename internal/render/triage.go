package render

import (
	"fmt"
	"time"

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
	// never appear as a row unless ShowHealthy is set.
	Checked int
	// Reports are the terminal table's rows, most severe first: unhealthy
	// resources only, or every swept resource when ShowHealthy is set. Each carries
	// the index it was saved under, since the table is a selection from All
	// and its rows need not be numbered 1..n — see TriageRow.
	Reports []TriageRow
	// All is every swept resource, most severe first, regardless of ShowHealthy
	// — the
	// HTML report shows the full inventory unconditionally, since its grid can
	// filter healthy rows away client-side rather than needing them dropped
	// before they ever reach the page.
	All []diagnostics.Report
	// Of is how many rows the table could have shown before a --limit cut
	// it, zero when nothing was cut. All and Reports then hold only the rows
	// kept.
	Of int
	// Worst is the worst verdict of the whole sweep, a --limit's cut
	// included: what kx diag --fail-on gates on.
	Worst   diagnostics.Severity
	Healthy int
	// ShowHealthy mirrors kx diag --show-healthy: Reports already includes the
	// healthy resources, so the footer must not also claim some were left out.
	ShowHealthy bool
	// Window is how far back the sweep was allowed to look. "12 checked"
	// and "all healthy" are both claims about what was looked at, and this
	// is the only line the table has to qualify them on. Zero means no
	// window was applied, and the caption says nothing.
	Window time.Duration
	// Match is the term the sweep was narrowed by, empty for none. Only the
	// empty sweep names it: there, "nothing to check" would claim the
	// namespace is empty when it may be full of resources that didn't match.
	Match string
}

// TriageRow is one row of the terminal table: a swept resource and the index
// that resolves it.
//
// The index is the report's 1-based position in All, the order the sweep was
// saved in, and not its position in the table. The two agree only while the
// unhealthy rows are a prefix of the sweep, which the severity ranking alone
// makes them; a --sort key interleaves healthy rows, so a table numbered by
// its own rows would hand out indexes resolving to the resources between them.
type TriageRow struct {
	Index int
	diagnostics.Report
}

// NothingMatches is the caption for a sweep a --match term narrowed to
// nothing — kx diag, kx scan and kx tree alike. Named for the term so the
// screen can't be mistaken for an empty namespace, which it may well not be.
func NothingMatches(term string) string {
	return "nothing matches '" + term + "'"
}

// Triage renders a namespace sweep: one row per unhealthy resource, indexed to
// match the saved state, with healthy resources collapsed into the footer —
// or one row per every swept resource, healthy included, when ShowHealthy is set.
//
// The caption follows the same "{kind} · {namespace} · {count} {noun}" shape as
// kx get and kx state, with "Mixed" as the kind since a sweep spans whatever
// kinds exist. It counts "checked" rather than "items" because the number is
// resources examined, not rows in the table below it.
func (r *Renderer) Triage(result TriageResult) {
	scope := result.Namespace
	if result.AllNamespaces {
		// The same words kx get -A captions itself with.
		scope = AllNamespaces
	}
	if result.Checked == 0 {
		if result.Match != "" {
			r.EmptyMatch("Mixed", scope, "0 checked", result.Match)
			return
		}
		r.Caption("Mixed", scope, "nothing to check")
		return
	}
	if len(result.Reports) == 0 {
		r.line(r.style(theme.Muted, "Mixed · "+scope+" · ") +
			r.style(theme.Success, strconv.Itoa(result.Checked)+" checked · all healthy") +
			r.style(theme.Muted, windowSuffix(result.Window)))
		return
	}

	// No blank line between the caption and the table: every other indexed
	// listing puts its header row directly under the caption, and the Python
	// renderer's extra line here was alone in doing otherwise.
	r.Caption("Mixed", scope, strconv.Itoa(result.Checked)+" checked", sweepShownLabel(result),
		WindowLabel(result.Window))

	// Headed "X" like every other indexed listing. The Python renderer left it
	// blank, alone among the indexed tables; these numbers are indexes passed
	// to `kx diag <index>` exactly as `kx get`'s are, so they are labeled the
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
	for _, row := range result.Reports {
		top := ""
		if len(row.Findings) > 0 {
			// One finding per line keeps the table scannable; the rest is one
			// `kx diag <index>` away.
			top = row.Findings[0].Summary
		}
		// The shapes share their last three cells; a cluster-wide sweep adds
		// the namespace between the kind and the name.
		lead := []Cell{
			Styled(strconv.Itoa(row.Index), theme.Muted),
			Plain(string(row.Kind)),
		}
		if result.AllNamespaces {
			lead = append(lead, Styled(row.Namespace, theme.Muted))
		}
		rows = append(rows, append(lead,
			Plain(ellipsize(row.Name, nameBudget)),
			Styled(row.Verdict.String(), severityStyle(row.Verdict)),
			Styled(top, theme.Body)))
	}
	r.Table(columns, rows)

	r.Blank()
	hint := "kx diag <index> for detail"
	footer := hint
	// Nothing hidden, nothing to say: --show-healthy hides none by definition, and a
	// sweep where nothing was healthy has no healthy count to withhold. The
	// footer used to count first and print regardless, so a namespace with
	// every resource unhealthy got "0 healthy resources not shown".
	if !result.ShowHealthy && result.Healthy > 0 {
		label := "resources"
		if result.Healthy == 1 {
			label = "resource"
		}
		footer = strconv.Itoa(result.Healthy) + " healthy " + label + " not shown · " + hint
	}
	r.line(r.style(theme.Muted, footer))
}

// sweepShownLabel is the caption's word on a --limit: "5 of 9 shown" when it cut
// the rows the table would have shown, and nothing when it did not.
func sweepShownLabel(result TriageResult) string {
	if result.Of <= len(result.Reports) {
		return ""
	}
	return strconv.Itoa(len(result.Reports)) + " of " + strconv.Itoa(result.Of) + " shown"
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
func widestNamespace(rows []TriageRow) int {
	widest := width("NAMESPACE")
	for _, row := range rows {
		if n := width(row.Namespace); n > widest {
			widest = n
		}
	}
	return widest
}

// Triage renders through the package-level renderer.
func Triage(result TriageResult) { current.Triage(result) }

// EmptyMatch renders a listing a --match term narrowed to nothing: the
// caption keeps the count label its command uses when it has results, at
// zero, and a single row beneath names the term that emptied it.
//
// The caption used to carry the explanation in place of the count, which made
// its shape depend on the result — the segment that holds a number held a
// sentence instead. The count belongs in the caption, where a reader scans
// for it; the reason belongs under it, where the rows would be.
//
// zeroCount is the caller's own noun at zero — "0 items", "0 checked",
// "0 images" — because each command already has one. kx tree prints no count
// when it has results, so it passes none and Caption drops the segment.
//
// A listing emptied with no --match term is not this: it has no term to name,
// so nothing would go in the row, and its caption keeps saying "none found".
func (r *Renderer) EmptyMatch(kind, scope, zeroCount, term string) {
	r.Caption(kind, scope, zeroCount)
	r.line(cellPad + r.style(theme.Muted, NothingMatches(term)))
}

// EmptyMatchErr is EmptyMatch on stderr, for a listing asked for in a format
// another program reads — see EmptyListingNotice for why.
func (r *Renderer) EmptyMatchErr(kind, scope, zeroCount, term string) {
	r.CaptionErr(kind, scope, zeroCount)
	fmt.Fprintln(r.err, cellPad+r.style(theme.Muted, NothingMatches(term)))
}

func EmptyMatch(kind, scope, zeroCount, term string) {
	current.EmptyMatch(kind, scope, zeroCount, term)
}
func EmptyMatchErr(kind, scope, zeroCount, term string) {
	current.EmptyMatchErr(kind, scope, zeroCount, term)
}
