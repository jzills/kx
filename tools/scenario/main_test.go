package main

import (
	"strings"
	"testing"
	"time"
)

// A fixture with an absolute date ages into meaninglessness, so the ages are
// written as offsets and resolved against the moment the scenario is applied.
func TestExpandTimestampsResolvesOffsetsAgainstNow(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	got, err := expandTimestamps(`finishedAt: "@-46d"`, now)
	if err != nil {
		t.Fatalf("expandTimestamps: %v", err)
	}
	if want := `finishedAt: "2026-07-21T12:00:00Z"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The same vocabulary kx diag --since reads, so a fixture and the flag that
// filters it cannot disagree about what an offset means.
func TestExpandTimestampsSpeaksTheSinceVocabulary(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	document, err := expandTimestamps("a: @-10m\nb: @-3h\nc: @-1d", now)
	if err != nil {
		t.Fatalf("expandTimestamps: %v", err)
	}
	for _, want := range []string{
		"2026-09-05T11:50:00Z", "2026-09-05T09:00:00Z", "2026-09-04T12:00:00Z",
	} {
		if !strings.Contains(document, want) {
			t.Errorf("document = %q, want it to contain %q", document, want)
		}
	}
}

// A typo in an offset must stop the apply rather than reach the cluster as a
// literal "@-46days", which the API would take as a malformed timestamp and
// reject with an error about the wrong thing.
func TestExpandTimestampsRejectsAnUnreadableOffset(t *testing.T) {
	if _, err := expandTimestamps(`finishedAt: "@-46days"`, time.Now()); err == nil {
		t.Fatal("expandTimestamps accepted @-46days")
	}
}

func TestExpandTimestampsLeavesADocumentWithoutOffsetsAlone(t *testing.T) {
	document := "kind: Pod\nname: ancient-crash\n"
	got, err := expandTimestamps(document, time.Now())
	if err != nil {
		t.Fatalf("expandTimestamps: %v", err)
	}
	if got != document {
		t.Errorf("got %q, want it unchanged", got)
	}
}

// `scenario list` reads its descriptions out of the READMEs so the listing
// cannot drift from the document someone actually opens.
//
// Chdir because the paths here are relative to the repository root, the way
// gen-site-docs's are: these tools are run as `go run ./tools/<name>` from
// the root, and a test binary starts in its own package directory.
func TestSummaryIsTheReadmeTitlesDescription(t *testing.T) {
	t.Chdir("../..")
	if got := summary("stale-history"); got == "" {
		t.Fatal("no summary read from the stale-history README")
	} else if strings.HasPrefix(got, "stale-history") {
		t.Errorf("summary = %q, want the description rather than the whole title", got)
	}
}
