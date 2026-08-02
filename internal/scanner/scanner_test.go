package scanner

import (
	"os"
	"path/filepath"
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

// realSarif is trimmed from `docker scout cves --format sarif nginx:1.21`
// (Scout 1.22.0, which emits 408 rules and 410 results), keeping one rule per
// case that matters: a rule with one package, a rule covering two packages
// whose results disagree about being fixed, and a Go module purl.
func realSarif(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scout.sarif.json"))
	if err != nil {
		t.Fatalf("reading fixture returned %v", err)
	}
	return string(raw)
}

func TestParseFindingsReadsEveryResult(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	// One finding per result, including the one whose rule index is bogus.
	if len(findings) != 5 {
		t.Fatalf("got %d findings, want 5", len(findings))
	}

	first := findings[0]
	if first.ID != "CVE-2021-22945" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Severity != "CRITICAL" {
		t.Errorf("Severity = %q", first.Severity)
	}
	if first.Package != "curl" {
		t.Errorf("Package = %q, want curl", first.Package)
	}
	// The purl version is percent-encoded; %2B is a plus.
	if first.Installed != "7.74.0-1.3+deb11u1" {
		t.Errorf("Installed = %q", first.Installed)
	}
	if first.FixedIn != "7.74.0-1.3+deb11u2" {
		t.Errorf("FixedIn = %q", first.FixedIn)
	}
	if first.URL != "https://scout.docker.com/v/CVE-2021-22945" {
		t.Errorf("URL = %q", first.URL)
	}
}

// A rule covering two packages emits two results whose fixed versions differ.
// Reading the fix from the rule would claim a fix for a package that has none.
func TestParseFindingsTakesFixedVersionPerResult(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var nghttp2, nginx Finding
	for _, f := range findings {
		switch f.Package {
		case "nghttp2":
			nghttp2 = f
		case "nginx":
			nginx = f
		}
	}
	if nghttp2.FixedIn != "1.43.0-1+deb11u1" {
		t.Errorf("nghttp2 FixedIn = %q", nghttp2.FixedIn)
	}
	// "not fixed" is prose, not a version. It must not reach the page as one.
	if nginx.FixedIn != "" {
		t.Errorf("nginx FixedIn = %q, want empty for an unfixed package", nginx.FixedIn)
	}
}

// A Go module keeps its path; collapsing it to the last segment would turn
// golang.org/x/crypto into "crypto".
func TestParseFindingsKeepsModulePaths(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Package == "golang.org/x/crypto" {
			found = true
			if f.Installed != "v0.28.0" {
				t.Errorf("Installed = %q", f.Installed)
			}
		}
	}
	if !found {
		t.Error("the Go module's full path was not preserved")
	}
}

// The counts the terminal table prints must not change.
func TestCountBySeverityMatchesTheOldTally(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	counts := CountBySeverity(findings)
	want := map[string]int{
		"CRITICAL": 1, "HIGH": 1, "MEDIUM": 0, "LOW": 2, "UNSPECIFIED": 1,
	}
	for severity, expected := range want {
		if counts[severity] != expected {
			t.Errorf("%s = %d, want %d", severity, counts[severity], expected)
		}
	}
}

func mustParseFindings(t *testing.T, document string) []Finding {
	t.Helper()
	findings, err := Scout{}.ParseFindings(document)
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	return findings
}

func TestParseCounts(t *testing.T) {
	counts := CountBySeverity(mustParseFindings(t, sarif))
	want := map[string]int{"CRITICAL": 2, "HIGH": 1, "MEDIUM": 1, "LOW": 0, "UNSPECIFIED": 1}
	for severity, expected := range want {
		if counts[severity] != expected {
			t.Errorf("%s = %d, want %d", severity, counts[severity], expected)
		}
	}
}

// Every bucket is present even at zero, so the table always has a full row.
func TestParseCountsAlwaysHasEveryBucket(t *testing.T) {
	counts := CountBySeverity(mustParseFindings(t, `{"runs":[]}`))
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
		counts := CountBySeverity(mustParseFindings(t, document))
		if counts["UNSPECIFIED"] != 1 {
			t.Errorf("UNSPECIFIED = %d, want 1 for %s", counts["UNSPECIFIED"], document)
		}
	}
}

// An unrecognized severity label falls into UNSPECIFIED rather than creating a
// bucket the table has no column for.
func TestParseCountsUnknownSeverity(t *testing.T) {
	counts := CountBySeverity(mustParseFindings(t,
		`{"runs":[{"tool":{"driver":{"rules":[{"properties":{"cvssV3_severity":"SEVERE"}}]}},`+
			`"results":[{"ruleIndex":0}]}]}`))
	if counts["UNSPECIFIED"] != 1 {
		t.Errorf("UNSPECIFIED = %d, want 1", counts["UNSPECIFIED"])
	}
	if len(counts) != len(Severities) {
		t.Errorf("counts has %d buckets, want %d", len(counts), len(Severities))
	}
}

func TestParseCountsRejectsGarbage(t *testing.T) {
	if _, err := (Scout{}).ParseFindings("not json"); err == nil {
		t.Error("ParseFindings accepted non-JSON")
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
