package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// DurationUnits and DurationExamples are the vocabulary this parser is
// documented with: the units a reader is told about, and one window per unit
// so none of them has to be inferred. Every --since help string, the flag's
// shell completion and the error below quote these rather than restating
// them, because three commands documenting the same flag had drifted to three
// near-identical lists — and because a list that short reads as exhaustive
// when it is only illustrative. `1s` has always parsed and nothing on screen
// said so.
//
// The parser takes more than it advertises. Go's sub-second units (ns, us,
// ms) are accepted and deliberately unnamed here: the finest window that
// means anything to a report about events and restarts is a second, and
// putting nanoseconds on a help screen costs every reader to serve none.
// Fractions and mixtures are real and useful, so the long help names them,
// where there is room for the one asymmetry — d takes a fraction but not a
// mixture.
const (
	DurationUnits    = "s, m, h or d"
	DurationExamples = "90s, 30m, 12h, 7d"
)

// ParseDuration parses a time window written the way a person writes one.
//
// Go's time.ParseDuration stops at hours, so `7d` — the spelling anyone asking
// for a week reaches for, and the one kubectl's own --since rejects — has to be
// handled here. Only a bare `<number>d` is understood, not a compound like
// `1d12h`: a day is where the vocabulary ends, and a compound form that mixed
// the two units would have to reimplement Go's scanner to earn it.
//
// Zero is legal and means "no window": something has to spell "show me
// everything", and 0 is what a duration flag conventionally uses for it.
// Negative is not — a window into the future is a typo, never an intent.
//
// It lives in config rather than next to the flag that parses it because the
// flag and the config key hold the same vocabulary, and a setting that read
// `7d` from the environment but not from the command line would be a trap.
func ParseDuration(value string) (time.Duration, error) {
	invalid := fmt.Errorf(
		"invalid duration %q — use a number and a unit ("+DurationUnits+"): "+
			DurationExamples, value)
	negative := fmt.Errorf("duration %q cannot be negative", value)

	parsed := time.Duration(0)
	if days, ok := strings.CutSuffix(value, "d"); ok {
		count, err := strconv.ParseFloat(days, 64)
		// NaN is a float64 ParseFloat accepts and a duration nobody typed:
		// "NaNd" parses, and converting it produces a number no comparison
		// below would catch.
		if err != nil || math.IsNaN(count) {
			return 0, invalid
		}
		if count < 0 {
			return 0, negative
		}
		// Checked before the conversion, not after: converting an
		// out-of-range float64 to an integer type is implementation-defined
		// in Go. On amd64 it wrapped, so "1e30d" — a plainly positive input
		// — came back as "cannot be negative", and on a platform that
		// saturates instead it would have quietly become a 292-year window.
		// ParseFloat accepts "Inf" too, which lands here rather than above.
		// Inclusive: maxDays is itself a count that does not fit, since
		// multiplying it by 24h lands on 2^63 rather than one below it.
		if count >= maxDays {
			return 0, fmt.Errorf(
				"duration %q is too long — a window has to fit in about 292 years", value)
		}
		parsed = time.Duration(count * float64(24*time.Hour))
	} else {
		// time.ParseDuration reports its own overflow, so the range check
		// above is only needed for the spelling it does not handle.
		var err error
		if parsed, err = time.ParseDuration(value); err != nil {
			return 0, invalid
		}
	}

	if parsed < 0 {
		return 0, negative
	}
	return parsed, nil
}

// maxDays is the longest window a time.Duration can hold, in days: int64
// nanoseconds, which runs out at roughly 292 years.
const maxDays = float64(math.MaxInt64) / float64(24*time.Hour)

// FormatDuration writes a window back in the vocabulary ParseDuration reads,
// so a window can be printed into a command line a reader could type.
//
// time.Duration's own String is unusable for that: it spells a week
// "168h0m0s". Each unit is used only when the window divides into it exactly,
// so nothing is rounded away — 36h stays 36h rather than becoming a day and a
// half.
func FormatDuration(window time.Duration) string {
	switch {
	case window == 0:
		return "0"
	// Days only from two up: a day-long window is written "24h" wherever it
	// is documented, and "1d" reads as a spelling nobody chose.
	case window >= 48*time.Hour && window%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", window/(24*time.Hour))
	case window%time.Hour == 0:
		return fmt.Sprintf("%dh", window/time.Hour)
	// Minutes alone only up to an hour. Past it the count stops being a
	// window anyone can picture — 25h30m came back as "1530m", a number the
	// reader has to divide before it means anything, on the banner, in the
	// JSON and on the HTML report's invocation line.
	case window < time.Hour && window%time.Minute == 0:
		return fmt.Sprintf("%dm", window/time.Minute)
	case window%time.Minute == 0:
		return fmt.Sprintf("%dh%dm", window/time.Hour, (window%time.Hour)/time.Minute)
	default:
		return window.String()
	}
}

// FormatSpan writes a measured duration coarsely: the largest unit it fills,
// and nothing below it.
//
// Distinct from FormatDuration, which spells a *configured* window and is built
// to round-trip through ParseDuration. A window is a round number somebody
// typed; a span is measured, so it is never round, and FormatDuration falls
// through to time.Duration's own String for it — an event aggregate 29 days
// wide came out as "695h59m0.000034494s".
//
// This is the spelling kx uses for every age and duration on screen ("2m ago",
// "for 24d"), so a span reads like the timestamps beside it. render.elapsed
// delegates here so there is one implementation rather than two that agree
// until they don't.
func FormatSpan(span time.Duration) string {
	seconds := int(span.Seconds())
	if seconds < 0 {
		seconds = 0
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
