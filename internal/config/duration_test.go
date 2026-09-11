package config

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The unit the reporter asked for. Go's own time.ParseDuration has no day, and
// neither does kubectl's --since, so `7d` is exactly what this parser exists
// to add.
func TestParseDurationAcceptsDays(t *testing.T) {
	got, err := ParseDuration("7d")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if want := 7 * 24 * time.Hour; got != want {
		t.Errorf("ParseDuration(\"7d\") = %v, want %v", got, want)
	}
}

func TestParseDurationAcceptsGoUnits(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"12h", 12 * time.Hour},
		{"90s", 90 * time.Second},
		{"1h30m", 90 * time.Minute},
	} {
		got, err := ParseDuration(tc.value)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// Zero is the spelling for "no window at all", so it has to survive the
// positive-value check that rejects a negative one.
func TestParseDurationAcceptsZero(t *testing.T) {
	got, err := ParseDuration("0")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 0 {
		t.Errorf("ParseDuration(\"0\") = %v, want 0", got)
	}
}

func TestParseDurationRejectsBadValues(t *testing.T) {
	for _, value := range []string{"", "7", "7x", "d", "-1h", "-2d", "seven days"} {
		if got, err := ParseDuration(value); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", value, got)
		}
	}
}

// The message has to name a spelling that works — an error that only says
// "invalid" leaves the reader guessing at the vocabulary.
func TestParseDurationErrorSuggestsAUnit(t *testing.T) {
	_, err := ParseDuration("7x")
	if err == nil {
		t.Fatal("ParseDuration(\"7x\") = nil error")
	}
	if !strings.Contains(err.Error(), "7d") {
		t.Errorf("error = %q, want it to show a valid spelling like 7d", err)
	}
}

// A day count large enough to overflow int64 nanoseconds converted to an
// implementation-defined value: on amd64 it wrapped negative, so a plainly
// positive input was reported as "cannot be negative", and on a platform that
// saturates instead it would have become a ~292-year window nobody asked for.
// ParseFloat also accepts "Inf" and "NaN", which are not durations either.
func TestParseDurationRejectsOutOfRangeDays(t *testing.T) {
	for _, value := range []string{"1e30d", "Infd", "NaNd", "1e9d"} {
		got, err := ParseDuration(value)
		if err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", value, got)
			continue
		}
		if strings.Contains(err.Error(), "negative") {
			t.Errorf("ParseDuration(%q) = %q, want a range error rather than a sign one",
				value, err)
		}
	}
}

// The largest window that fits stays legal: the ceiling is int64 nanoseconds,
// not a number somebody guessed at.
func TestParseDurationAcceptsTheLargestWindowThatFits(t *testing.T) {
	if got, err := ParseDuration("100000d"); err != nil {
		t.Errorf("ParseDuration(\"100000d\") = %v, want 273 years and no error", err)
	} else if got != 100000*24*time.Hour {
		t.Errorf("ParseDuration(\"100000d\") = %v, want 100000 days", got)
	}
}

// FormatDuration is what the HTML report's invocation line prints, so a
// window has to come back out in the vocabulary it went in as: 168h0m0s is
// not a command anyone typed.
func TestFormatDurationRoundTripsTheSpelling(t *testing.T) {
	for _, tc := range []struct {
		value time.Duration
		want  string
	}{
		{7 * 24 * time.Hour, "7d"},
		{48 * time.Hour, "2d"},
		{36 * time.Hour, "36h"},
		// A day is spelled in hours: "24h" is how the default is written
		// everywhere it is documented, and how anyone says a day-long window.
		{24 * time.Hour, "24h"},
		{12 * time.Hour, "12h"},
		// Past an hour, minutes alone stop being readable: this one used to
		// come back as "1530m", a number the reader has to divide before it
		// means anything.
		{25*time.Hour + 30*time.Minute, "25h30m"},
		{90 * time.Minute, "1h30m"},
		// Under an hour there are no hours to name, and "30m" is the
		// spelling the docs teach.
		{30 * time.Minute, "30m"},
		{59 * time.Minute, "59m"},
		{90 * time.Second, "1m30s"},
		{0, "0"},
	} {
		if got := FormatDuration(tc.value); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// maxDays itself is a day count that does not fit: multiplying it by 24h
// lands on 2^63, one past what an int64 holds. The guard let it through —
// `count > maxDays` is false for maxDays — so it reached the conversion,
// wrapped negative, and came back as `cannot be negative`, the sign error the
// guard exists to keep a plainly positive input from producing.
//
// Computed rather than written out. The boundary is whatever MaxInt64 over
// 24h rounds to, and a literal here would only be checking the literal.
func TestParseDurationRejectsTheBoundaryItCannotHold(t *testing.T) {
	value := strconv.FormatFloat(maxDays, 'g', -1, 64) + "d"
	got, err := ParseDuration(value)
	if err == nil {
		t.Fatalf("ParseDuration(%q) = %v, want an error: it does not fit in a Duration",
			value, got)
	}
	if strings.Contains(err.Error(), "negative") {
		t.Errorf("ParseDuration(%q) = %q, want a range error rather than a sign one",
			value, err)
	}
}

// The largest day count that does fit is still accepted: the guard is a
// boundary, not a retreat from it.
func TestParseDurationAcceptsTheDayCountJustInsideTheBoundary(t *testing.T) {
	value := strconv.FormatFloat(math.Nextafter(maxDays, 0), 'g', -1, 64) + "d"
	got, err := ParseDuration(value)
	if err != nil {
		t.Fatalf("ParseDuration(%q) = %v, want the largest window that fits", value, err)
	}
	if got <= 0 {
		t.Errorf("ParseDuration(%q) = %v, want a positive duration", value, got)
	}
}

// The round trip the spelling exists for: whatever FormatDuration writes,
// ParseDuration has to read back as the same window. Both are on the path
// from a --since value to the banner, the JSON and the HTML invocation line,
// and a spelling that did not survive it would put a command on screen that
// kx itself rejects.
func TestFormatDurationSurvivesParseDuration(t *testing.T) {
	for _, window := range []time.Duration{
		0,
		30 * time.Second,
		90 * time.Second,
		10 * time.Second,
		30 * time.Minute,
		59 * time.Minute,
		90 * time.Minute,
		25*time.Hour + 30*time.Minute,
		time.Hour + 30*time.Second,
		12 * time.Hour,
		24 * time.Hour,
		36 * time.Hour,
		7 * 24 * time.Hour,
	} {
		spelled := FormatDuration(window)
		got, err := ParseDuration(spelled)
		if err != nil {
			t.Errorf("ParseDuration(FormatDuration(%v)) = %v, and %q is on screen as a "+
				"command to type", window, err, spelled)
			continue
		}
		if got != window {
			t.Errorf("ParseDuration(FormatDuration(%v)) = %v via %q, want the window back",
				window, got, spelled)
		}
	}
}
