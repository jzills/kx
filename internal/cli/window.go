package cli

import (
	"fmt"
	"time"

	"github.com/jzills/kx/internal/config"
)

// A --since flag and the setting behind it, shared by the commands that bound
// how far back they look. kx diag and kx events read different settings —
// diag_max_age and events_max_age — but the flag means the same thing on both,
// down to how "unset" and "0" are told apart, so the resolution and the help
// are written once here rather than per command.

// resolveWindow resolves a --since value against the setting the flag falls
// back to when it is absent.
//
// An empty value means the flag was absent — "" is not a duration anyone can
// type, so no Changed() check is needed to tell "unset" from "0", and
// `--since 0` keeps its own meaning of no window at all.
func resolveWindow(since string, configured time.Duration) (time.Duration, error) {
	if since == "" {
		return configured, nil
	}
	window, err := config.ParseDuration(since)
	if err != nil {
		return 0, fmt.Errorf("'--since': %w", err)
	}
	return window, nil
}

// sinceUsage builds a --since flag's help: what the flag does, then the setting
// it defaults to and the value that setting currently holds.
//
// The default is read rather than described, because a help screen that
// describes it can only describe one machine's. kx diag's said "which is unset"
// on every machine, including the ones where the key was set — the only readers
// for whom the default is not obvious. Spelled with FormatDuration, so the help
// names a value that can be typed straight back at the flag.
func sinceUsage(lead, key string, configured time.Duration, unbounded string) string {
	if configured == 0 {
		return lead + " Defaults to " + key + ", which is unset: " + unbounded
	}
	return lead + " Defaults to " + key + ", currently " + config.FormatDuration(configured)
}
