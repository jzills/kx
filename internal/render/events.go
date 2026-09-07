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

// FormatElapsed renders how long something has been true ("24d"), where
// FormatAge renders when something happened ("24d ago").
//
// A report says one or the other about every finding, and never both: a
// moment can be filtered away by --since, a duration cannot. The magnitude
// is shared so the two read as one scale; only the "ago" separates them.
func FormatElapsed(since time.Time) string { return elapsed(time.Now(), since) }

// FormatElapsedAt is FormatElapsed against an explicit "now", for the HTML
// renderer — see FormatAgeAt.
func FormatElapsedAt(now, since time.Time) string { return elapsed(now, since) }

// formatAgeAt takes the reference time so the formatting is testable without
// freezing the clock.
func formatAgeAt(now, timestamp time.Time) string {
	magnitude := elapsed(now, timestamp)
	if magnitude == "" || magnitude == justNow {
		return magnitude
	}
	return magnitude + " ago"
}

// justNow is what a timestamp in the future renders as, rather than "in 3m":
// the cause is clock skew between the API server and here, and admitting
// nothing useful is known beats reporting a negative age. It carries no
// "ago", which is why formatAgeAt passes it through untouched.
const justNow = "just now"

func elapsed(now, timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	seconds := int(now.Sub(timestamp).Seconds())
	if seconds < 0 {
		return justNow
	}
	for _, unit := range []struct {
		suffix string
		size   int
	}{{"d", 86400}, {"h", 3600}, {"m", 60}} {
		if seconds >= unit.size {
			return fmt.Sprintf("%d%s", seconds/unit.size, unit.suffix)
		}
	}
	return fmt.Sprintf("%ds", seconds)
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
