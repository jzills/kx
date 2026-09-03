package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
	r.line(r.style(theme.Muted, prefix) + extra)

	r.Blank()
	// Section headers align with the pod table's content, which pads by two.
	r.line("  " + r.style(theme.Header, "SUMMARY"))
	if len(report.Findings) == 0 {
		r.line("    " + r.style(theme.Muted, "No issues detected"))
	} else {
		for _, finding := range report.Findings {
			icon := r.style(severityStyle(finding.Severity), severityIcon(finding.Severity))
			r.line("  " + icon + " " + r.style(theme.Body, finding.Summary))
		}
	}

	r.podTable(report.Pods)
	r.logs(report.Pods)
	r.warningEvents(report.WarningEvents)
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
				Plain(strconv.Itoa(int(container.RestartCount))),
				Plain(container.Name),
				Styled(container.State, statusColor(container.State)),
				reasonCell,
			})
		}
	}
	r.Table(columns, rows)
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
		if !e.container.LogFiltered {
			// Nothing matched a severity token, so this is just the tail.
			note = " · recent output"
		}
		r.line("    " + r.style(theme.Muted,
			"Pod/"+e.pod.Name+" · container "+e.container.Name+note))
		for _, line := range e.container.LogLines {
			r.line("      " + r.highlightSeverity(line))
		}
	}
}

func (r *Renderer) warningEvents(events []diagnostics.EventSummary) {
	r.Blank()
	r.line("  " + r.style(theme.Header, "WARNING EVENTS"))
	if len(events) == 0 {
		r.line("    " + r.style(theme.Muted, "No warning events"))
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
			r.style(theme.Muted, " ×"+strconv.Itoa(int(event.Count)))
		if age := FormatAge(event.LastTimestamp); age != "" {
			line += r.style(theme.Muted, " · "+age)
		}
		r.line("    " + line)
		r.line("      " + event.Message)
	}
}

// Diagnostic renders through the package-level renderer.
func Diagnostic(report diagnostics.Report) { current.Diagnostic(report) }

// severityTokenPattern matches the OTEL severity tokens highlighted in log
// lines. Kept here rather than shared with the diagnostics package so the
// renderer has no reason to import the matcher's filtering behaviour.
var severityTokenPattern = regexp.MustCompile(
	`(?i)\b(FATAL|CRITICAL|ERROR|ERR|WARNING|WARN|EXCEPTION|TRACEBACK|PANIC)\b`)
