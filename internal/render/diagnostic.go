package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jzills/kx/internal/config"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/theme"
)

// severityIcon and severityStyle map a severity onto its marker and style.
func severityIcon(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return "✗"
	case diagnostics.Warning:
		return "!"
	default:
		return "✓"
	}
}

func severityStyle(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return theme.StatusBad
	case diagnostics.Warning:
		return theme.StatusWarn
	default:
		return theme.StatusOK
	}
}

// WindowLabel spells the window a report was gathered under, for a caption
// that has to say what it was allowed to see. Empty when there is no window.
//
// The same vocabulary the --since flag reads, so the caption names a value
// that can be typed straight back at it. Exported for the HTML report, which
// has the same thing to say and must not invent its own spelling for it.
func WindowLabel(window time.Duration) string {
	if window <= 0 {
		return ""
	}
	return "last " + config.FormatDuration(window)
}

// windowSuffix is WindowLabel as a trailing segment, for the lines that build
// their own caption rather than going through Caption.
func windowSuffix(window time.Duration) string {
	if label := WindowLabel(window); label != "" {
		return " · " + label
	}
	return ""
}

// A finding and an event message are both sentences the cluster wrote, and
// Kubernetes writes long ones — the scheduler's "0/1 nodes are available…"
// runs past 200 columns. Wrapped here rather than left to the terminal, which
// breaks a line at column 0: the continuation then starts to the left of the
// section header and the block stops reading as a list at all.
//
// Each continuation is tucked one level inside the text it belongs to, so the
// icon (or the event heading) keeps the left margin to itself and the eye can
// still find where one entry ends and the next begins.
const (
	findingHang      = "      "
	eventMessageHang = "        "
)

// proseLines wraps text to what is left of the prose width once a hanging
// indent of hang columns is paid for, so the first line and every
// continuation fit the same budget.
func (r *Renderer) proseLines(text string, hang int) []string {
	return wrapText(text, r.proseWidth()-hang)
}

// Diagnostic renders a full report for one resource.
func (r *Renderer) Diagnostic(report diagnostics.Report) {
	// The verdict rides in the banner rather than on a line of its own.
	status := r.style(severityStyle(report.Verdict),
		severityIcon(report.Verdict)+" "+report.Verdict.String())
	extra := status
	if count := len(report.Findings); count > 0 {
		label := "issues"
		if count == 1 {
			label = "issue"
		}
		extra = status + " · " + strconv.Itoa(count) + " " + label
	}
	// Built by hand rather than through Caption, so the verdict keeps its own
	// colour inside the muted banner.
	// The namespace segment is dropped when there isn't one rather than left
	// as an empty gap between separators — a Node is cluster-scoped, and
	// "Node/x ·  · healthy" reads as a missing value rather than an absent
	// one. Caption drops empty parts for the same reason; this line builds its
	// own prefix because the verdict is styled separately.
	prefix := string(report.Kind) + "/" + report.Name + " · "
	if report.Namespace != "" {
		prefix += report.Namespace + " · "
	}
	r.line(r.style(theme.Muted, prefix) + extra +
		r.style(theme.Muted, windowSuffix(report.Window)))

	r.Blank()
	// Section headers align with the pod table's content, which pads by two.
	r.line("  " + r.style(theme.Header, "SUMMARY"))
	if len(report.Findings) == 0 {
		r.line("    " + r.style(theme.Muted, "No issues detected"))
	} else {
		for _, finding := range report.Findings {
			icon := r.style(severityStyle(finding.Severity), severityIcon(finding.Severity))
			lines := r.findingLines(finding)
			r.line("  " + icon + " " + lines[0])
			for _, rest := range lines[1:] {
				r.line(findingHang + rest)
			}
		}
	}

	r.podTable(report.Pods)
	r.logs(report.Pods)
	r.warningEvents(report.WarningEvents, report.Window)
}

// podTable lists every pod and its containers, one container per row with the
// pod's own cells left blank after the first.
func (r *Renderer) podTable(pods []diagnostics.PodDiagnostic) {
	if len(pods) == 0 {
		return
	}
	r.Blank()

	columns := []Column{
		{Header: "POD"}, {Header: "PHASE"}, {Header: "READY"}, {Header: "RESTARTS"},
		{Header: "CONTAINER"}, {Header: "STATE"}, {Header: "REASON"},
	}
	var rows [][]Cell
	for _, pod := range pods {
		ready := fmt.Sprintf("%d/%d", pod.ReadyContainers, pod.TotalContainers)
		phase := Styled(pod.Phase, statusColor(pod.Phase))
		if len(pod.Containers) == 0 {
			rows = append(rows, []Cell{
				Plain(pod.Name), phase, Plain(ready), Plain(""), Plain(""), Plain(""), Plain(""),
			})
			continue
		}
		for offset, container := range pod.Containers {
			reason := container.WaitingReason
			if reason == "" {
				reason = container.TerminatedReason
			}
			reasonCell := Plain("")
			if reason != "" {
				reasonCell = Styled(reason, statusColor(reason))
			}
			name, phaseCell, readyCell := Plain(""), Plain(""), Plain("")
			if offset == 0 {
				name, phaseCell, readyCell = Plain(pod.Name), phase, Plain(ready)
			}
			rows = append(rows, []Cell{
				name, phaseCell, readyCell,
				Plain(restarts(container)),
				Plain(container.Name),
				Styled(containerState(container), statusColor(container.State)),
				reasonCell,
			})
		}
	}
	r.Table(columns, rows)
}

// restarts spells the restart count with the time of the last one, the way
// kubectl get pods does — "21 (3h ago)". The count alone is cumulative over
// the pod's whole life, so it cannot say whether the thrashing is current,
// which is the question the window turns on.
func restarts(container diagnostics.ContainerDiagnostic) string {
	count := strconv.Itoa(int(container.RestartCount))
	if container.RestartCount == 0 {
		return count
	}
	if age := FormatAge(container.LastTerminatedAt); age != "" {
		return count + " (" + age + ")"
	}
	return count
}

// eventSpan says how long an aggregated event's ×count took to accumulate, so
// the tally can be read. Empty when the API dated only one end of it, or when
// there was a single occurrence with nothing to span.
//
// "over", not "for": the trailing "· for 24d" below is reserved for how long
// something has been true, which no window hides. This is a tally's span, and
// it sits inside the line rather than at the end of it.
func eventSpan(event diagnostics.EventSummary) string {
	if span := event.Span(); span != "" {
		return " over " + span
	}
	return ""
}

// findingTime is the trailing segment that says when a finding's subject
// happened, or how long it has been true.
//
// Two readings, one shape: "· 2m ago" is a moment, and a narrow enough
// --since will hide that finding; "· for 24d" is a duration, and no window
// ever will. The words carry that difference, so the separator does not have
// to — every other trailing segment kx prints is · -separated, and a
// parenthetical here would be the only one of its kind on the screen.
//
// A finding carries one or neither, never both, and neither when the cluster
// records no way to date it.
func findingTime(f diagnostics.Finding) string {
	if moment := FormatAge(f.At); moment != "" {
		return " · " + moment
	}
	if duration := FormatElapsed(f.Since); duration != "" {
		return " · for " + duration
	}
	return ""
}

// findingLines is one finding as the lines it renders on: its summary wrapped
// to the prose width, with the moment or duration from findingTime on the end
// of the last line — or on a line of its own when that line has no room left.
//
// Inside the wrapping rather than appended after it, because the time is part
// of the sentence: appended, it would be the one thing in the block still able
// to run past the width the rest of it was just wrapped to.
func (r *Renderer) findingLines(finding diagnostics.Finding) []string {
	wrapped := r.proseLines(finding.Summary, len(findingHang))
	lines := make([]string, len(wrapped))
	for i, text := range wrapped {
		lines[i] = r.style(theme.Body, text)
	}

	suffix := findingTime(finding)
	if suffix == "" {
		return lines
	}
	last := len(wrapped) - 1
	if len(wrapped[last])+len(suffix) <= r.proseWidth()-len(findingHang) {
		lines[last] += r.style(theme.Muted, suffix)
		return lines
	}
	// No room, so the time takes a line of its own — still the entry's own
	// indent, and still carrying its separator, since what it separates is
	// the summary above it rather than a neighbour on the same line.
	return append(lines, r.style(theme.Muted, strings.TrimPrefix(suffix, " ")))
}

// containerState names the state with the moment it stopped, when it has
// stopped — "Terminated (46d ago)".
//
// A report can read healthy with corpses still in its table, since findings
// about a container that finished before the window are dropped while the
// table goes on listing what exists. The age is what keeps those two honest
// with each other.
func containerState(container diagnostics.ContainerDiagnostic) string {
	if age := FormatAge(container.TerminatedAt); age != "" {
		return container.State + " (" + age + ")"
	}
	return container.State
}

// Log tokens that read as failures rather than warnings.
var logErrorTokens = map[string]bool{
	"FATAL": true, "CRITICAL": true, "ERROR": true, "ERR": true,
	"EXCEPTION": true, "TRACEBACK": true, "PANIC": true,
}

// highlightSeverity colours the OTEL severity tokens inside a log line.
func (r *Renderer) highlightSeverity(line string) string {
	return severityTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
		style := theme.Warn
		if logErrorTokens[strings.ToUpper(token)] {
			style = theme.Error
		}
		return r.style(style, token)
	})
}

func (r *Renderer) logs(pods []diagnostics.PodDiagnostic) {
	type entry struct {
		pod       diagnostics.PodDiagnostic
		container diagnostics.ContainerDiagnostic
	}
	var entries []entry
	for _, pod := range pods {
		for _, container := range pod.Containers {
			if len(container.LogLines) > 0 {
				entries = append(entries, entry{pod, container})
			}
		}
	}
	if len(entries) == 0 {
		return
	}

	r.Blank()
	r.line("  " + r.style(theme.Header, "LOGS"))
	for _, e := range entries {
		note := ""
		// A tail from a dead instance is an excerpt of a crash that
		// happened at some point; without its age an old one reads as the
		// current failure.
		if e.container.LogSource == "previous" {
			if age := FormatAge(e.container.LastTerminatedAt); age != "" {
				note = " · previous instance, " + age
			}
		}
		if !e.container.LogFiltered {
			// Nothing matched a severity token, so this is just the tail.
			note += " · recent output"
		}
		r.line("    " + r.style(theme.Muted,
			"Pod/"+e.pod.Name+" · container "+e.container.Name+note))
		for _, line := range e.container.LogLines {
			r.line("      " + r.highlightSeverity(line))
		}
	}
}

func (r *Renderer) warningEvents(events []diagnostics.EventSummary, window time.Duration) {
	r.Blank()
	r.line("  " + r.style(theme.Header, "WARNING EVENTS"))
	if len(events) == 0 {
		// Qualified when a window is in force: "No warning events" would
		// otherwise mean both "there are none" and "there are, and they
		// were older than the window", and only one of those is reassuring.
		empty := "No warning events"
		if label := WindowLabel(window); label != "" {
			empty += " in the " + label
		}
		r.line("    " + r.style(theme.Muted, empty))
		return
	}
	for position, event := range events {
		// A blank line separates events from each other, not from the header.
		if position > 0 {
			r.Blank()
		}
		// Object first, matching the LOGS subheadings.
		line := r.style(theme.Muted, event.Kind+"/"+event.Name+" · ") +
			r.style(statusColor(event.Reason), event.Reason) +
			r.style(theme.Muted, " ×"+strconv.Itoa(int(event.Count))+eventSpan(event))
		if age := FormatAge(event.LastTimestamp); age != "" {
			line += r.style(theme.Muted, " · "+age)
		}
		r.line("    " + line)
		message := r.proseLines(event.Message, len(eventMessageHang))
		r.line("      " + message[0])
		for _, rest := range message[1:] {
			r.line(eventMessageHang + rest)
		}
	}
}

// Diagnostic renders through the package-level renderer.
func Diagnostic(report diagnostics.Report) { current.Diagnostic(report) }

// severityTokenPattern matches the OTEL severity tokens highlighted in log
// lines. Kept here rather than shared with the diagnostics package so the
// renderer has no reason to import the matcher's filtering behaviour.
var severityTokenPattern = regexp.MustCompile(
	`(?i)\b(FATAL|CRITICAL|ERROR|ERR|WARNING|WARN|EXCEPTION|TRACEBACK|PANIC)\b`)
