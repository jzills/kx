package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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
		"invalid duration %q — use a number and a unit, such as 30m, 12h or 7d", value)

	parsed := time.Duration(0)
	if days, ok := strings.CutSuffix(value, "d"); ok {
		count, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, invalid
		}
		parsed = time.Duration(count * float64(24*time.Hour))
	} else {
		var err error
		if parsed, err = time.ParseDuration(value); err != nil {
			return 0, invalid
		}
	}

	if parsed < 0 {
		return 0, fmt.Errorf("duration %q cannot be negative", value)
	}
	return parsed, nil
}

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
	case window%time.Minute == 0:
		return fmt.Sprintf("%dm", window/time.Minute)
	default:
		return window.String()
	}
}
