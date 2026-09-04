package config

import (
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

// FormatDuration is what the HTML report's invocation line prints, so a
// window has to come back out in the vocabulary it went in as: 168h0m0s is
// not a command anyone typed.
func TestFormatDurationRoundTripsTheSpelling(t *testing.T) {
	for _, tc := range []struct {
		value time.Duration
		want  string
	}{
		{7 * 24 * time.Hour, "7d"},
		{36 * time.Hour, "36h"},
		{24 * time.Hour, "1d"},
		{12 * time.Hour, "12h"},
		{30 * time.Minute, "30m"},
		{90 * time.Second, "1m30s"},
		{0, "0"},
	} {
		if got := FormatDuration(tc.value); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
