package scanner

import (
	"strings"
	"testing"
)

// SARIF puts severities on the rules and references them by index from the
// results, so counting means resolving that indirection.
const sarif = `{
  "runs": [{
    "tool": {"driver": {"rules": [
      {"properties": {"cvssV3_severity": "CRITICAL"}},
      {"properties": {"cvssV3_severity": "high"}},
      {"properties": {"cvssV3_severity": "MEDIUM"}},
      {"properties": {}}
    ]}},
    "results": [
      {"ruleIndex": 0}, {"ruleIndex": 0}, {"ruleIndex": 1},
      {"ruleIndex": 2}, {"ruleIndex": 3}
    ]
  }]
}`

func TestParseCounts(t *testing.T) {
	counts, err := Scout{}.ParseCounts(sarif)
	if err != nil {
		t.Fatalf("ParseCounts: %v", err)
	}
	want := map[string]int{"CRITICAL": 2, "HIGH": 1, "MEDIUM": 1, "LOW": 0, "UNSPECIFIED": 1}
	for severity, expected := range want {
		if counts[severity] != expected {
			t.Errorf("%s = %d, want %d", severity, counts[severity], expected)
		}
	}
}

// Every bucket is present even at zero, so the table always has a full row.
func TestParseCountsAlwaysHasEveryBucket(t *testing.T) {
	counts, err := Scout{}.ParseCounts(`{"runs":[]}`)
	if err != nil {
		t.Fatalf("ParseCounts: %v", err)
	}
	for _, severity := range Severities {
		if _, ok := counts[severity]; !ok {
			t.Errorf("bucket %q is missing", severity)
		}
	}
}

// A result whose rule index is missing or out of range still counts, as
// UNSPECIFIED — dropping it would make the totals disagree with the scan.
func TestParseCountsHandlesBadRuleIndexes(t *testing.T) {
	cases := []string{
		`{"runs":[{"tool":{"driver":{"rules":[]}},"results":[{"ruleIndex":5}]}]}`,
		`{"runs":[{"tool":{"driver":{"rules":[]}},"results":[{}]}]}`,
		`{"runs":[{"tool":{"driver":{"rules":[]}},"results":[{"ruleIndex":-1}]}]}`,
	}
	for _, document := range cases {
		counts, err := Scout{}.ParseCounts(document)
		if err != nil {
			t.Fatalf("ParseCounts: %v", err)
		}
		if counts["UNSPECIFIED"] != 1 {
			t.Errorf("UNSPECIFIED = %d, want 1 for %s", counts["UNSPECIFIED"], document)
		}
	}
}

// An unrecognized severity label falls into UNSPECIFIED rather than creating a
// bucket the table has no column for.
func TestParseCountsUnknownSeverity(t *testing.T) {
	counts, err := Scout{}.ParseCounts(
		`{"runs":[{"tool":{"driver":{"rules":[{"properties":{"cvssV3_severity":"SEVERE"}}]}},` +
			`"results":[{"ruleIndex":0}]}]}`)
	if err != nil {
		t.Fatalf("ParseCounts: %v", err)
	}
	if counts["UNSPECIFIED"] != 1 {
		t.Errorf("UNSPECIFIED = %d, want 1", counts["UNSPECIFIED"])
	}
	if len(counts) != len(Severities) {
		t.Errorf("counts has %d buckets, want %d", len(counts), len(Severities))
	}
}

func TestParseCountsRejectsGarbage(t *testing.T) {
	if _, err := (Scout{}).ParseCounts("not json"); err == nil {
		t.Error("ParseCounts accepted non-JSON")
	}
}

func TestScoutArgv(t *testing.T) {
	scout := Scout{}
	if got := scout.SummaryArgv("nginx:1.25"); len(got) != 6 || got[4] != "sarif" {
		t.Errorf("SummaryArgv = %v, want a sarif request", got)
	}
	got := scout.PassthroughArgv("nginx:1.25", []string{"--only-fixed"})
	if got[len(got)-1] != "--only-fixed" {
		t.Errorf("PassthroughArgv = %v, want the extra flag forwarded", got)
	}
	// A plain Docker install answers this with "unknown command" and exits 1.
	if got := scout.PreflightArgv(); got[1] != "scout" {
		t.Errorf("PreflightArgv = %v", got)
	}
}

func TestGetEngine(t *testing.T) {
	if _, err := GetEngine("scout"); err != nil {
		t.Errorf("GetEngine(scout): %v", err)
	}
	_, err := GetEngine("nonexistent")
	if err == nil {
		t.Fatal("GetEngine accepted an unknown engine")
	}
	// The message names what is available, so a typo is self-correcting.
	if got := err.Error(); !strings.Contains(got, "scout") {
		t.Errorf("error = %q, want it to list the known engines", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
