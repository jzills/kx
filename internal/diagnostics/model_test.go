package diagnostics

import (
	"testing"
	"time"
)

// An aggregated Event is one object carrying a tally and two timestamps: the
// API folds repeats into it rather than writing one object per occurrence. So
// the ×count spans first..last, and on a real cluster that is not a detail —
// a BackOff on a stuck image pull reached ×52122 across 29 days.
//
// Without the span the number cannot be read at all. ×52122 over 29 days is
// ~75/hour, a pod nobody fixed; ×52122 in an hour is a meltdown. The window
// made this worse rather than caused it: --since 1h captioned the report "last
// 1h" above a count covering 29 days.
func TestEventSummarySpansItsAggregate(t *testing.T) {
	first := time.Date(2026, 8, 13, 1, 20, 0, 0, time.UTC)
	summary := EventSummary{
		Count: 52122, FirstTimestamp: first, LastTimestamp: first.Add(29 * 24 * time.Hour),
	}
	if got := summary.Span(); got != "29d" {
		t.Errorf("Span() = %q, want \"29d\"", got)
	}
}

// A single occurrence has nothing to span, and a quiet cluster must stay quiet:
// "×1 over 0s" is noise on every healthy line.
func TestEventSummaryWithNothingToSpanSaysNothing(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for name, summary := range map[string]EventSummary{
		"single occurrence": {Count: 1, FirstTimestamp: at, LastTimestamp: at},
		"no first":          {Count: 9, LastTimestamp: at},
		"no last":           {Count: 9, FirstTimestamp: at},
		"neither":           {Count: 9},
	} {
		if got := summary.Span(); got != "" {
			t.Errorf("%s: Span() = %q, want empty", name, got)
		}
	}
}

// A span is measured, not typed, so it is never round. Spelled through
// FormatDuration — which exists to round-trip a *configured* window — a 29-day
// aggregate came back as "695h59m0.000034494s". FormatSpan gives the largest
// unit it fills and nothing below, which is how kx spells every other age on
// the screen.
func TestEventSummarySpanIsSpelledCoarsely(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		span time.Duration
		want string
	}{
		{7 * 24 * time.Hour, "7d"},
		{36 * time.Hour, "1d"},
		{90 * time.Minute, "1h"},
		{45 * time.Second, "45s"},
		// The shape that started this: 29 days and change, not a round number.
		{29*24*time.Hour + 59*time.Minute + 34494*time.Nanosecond, "29d"},
	} {
		summary := EventSummary{Count: 9, FirstTimestamp: at, LastTimestamp: at.Add(tc.span)}
		if got := summary.Span(); got != tc.want {
			t.Errorf("Span() for %v = %q, want %q", tc.span, got, tc.want)
		}
	}
}

// Clock skew between the API server and here can land last before first. A
// negative span is never printed — the same rule justNow follows for ages.
func TestEventSummarySpanIgnoresSkew(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	summary := EventSummary{Count: 9, FirstTimestamp: at, LastTimestamp: at.Add(-time.Hour)}
	if got := summary.Span(); got != "" {
		t.Errorf("Span() = %q for a backwards span, want empty", got)
	}
}
