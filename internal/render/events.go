package render

import (
	"fmt"
	"time"

	"github.com/jzills/kx/internal/events"
	"github.com/jzills/kx/internal/theme"
)

// FormatAge renders a timestamp as a compact age ("3m ago").
func FormatAge(timestamp time.Time) string {
	return formatAgeAt(time.Now(), timestamp)
}

// FormatAgeAt formats an age relative to an explicit "now", so a caller that
// must render deterministically — the HTML renderer, whose output is compared
// byte-for-byte — can pin the reference time instead of reading the clock.
func FormatAgeAt(now, timestamp time.Time) string { return formatAgeAt(now, timestamp) }

// formatAgeAt takes the reference time so the formatting is testable without
// freezing the clock.
func formatAgeAt(now, timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	seconds := int(now.Sub(timestamp).Seconds())
	if seconds < 0 {
		// Clock skew between the API server and here; "in 3m" would be worse
		// than admitting nothing useful is known.
		return "just now"
	}
	for _, unit := range []struct {
		suffix string
		size   int
	}{{"d", 86400}, {"h", 3600}, {"m", 60}} {
		if seconds >= unit.size {
			return fmt.Sprintf("%d%s ago", seconds/unit.size, unit.suffix)
		}
	}
	return fmt.Sprintf("%ds ago", seconds)
}

// EventsTable renders the events for one resource.
func (r *Renderer) EventsTable(rows []events.Row) {
	if len(rows) == 0 {
		r.Caption("No events found")
		return
	}

	columns := []Column{
		{Header: "TYPE"}, {Header: "REASON"}, {Header: "KIND"},
		{Header: "AGE"}, {Header: "MESSAGE"},
	}
	cells := make([][]Cell, 0, len(rows))
	for _, row := range rows {
		// Normal events are context; Warnings are what the user came for.
		typeStyle := theme.Warn
		if row.Type == "Normal" {
			typeStyle = theme.Muted
		}
		cells = append(cells, []Cell{
			Styled(row.Type, typeStyle),
			Plain(row.Reason),
			Plain(row.Kind),
			Styled(FormatAge(row.Timestamp), theme.Muted),
			Plain(row.Message),
		})
	}
	r.Table(columns, cells)
}

// EventsTable renders through the package-level renderer.
func EventsTable(rows []events.Row) { current.EventsTable(rows) }
