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

// realSarif is derived from `docker scout cves --format sarif nginx:1.21`
// (Scout 1.22.0, which emits 408 rules and 410 results): the curl,
// nghttp2/nginx and golang.org/x/crypto rules and their results are real,
// trimmed to one rule per case that matters — a rule with one package, a
// rule covering two packages whose results disagree about being fixed, and a
// Go module purl. The rest are synthetic, added to exercise cases the trimmed
// real document doesn't happen to contain: CVE-9999-0000 is an out-of-range
// rule index; a third result against the nghttp2/nginx rule names a package
// ("libssl1.1") the rule never declared and omits "Fixed version" entirely,
// so the rule's fixed_version has nothing valid to leak onto; and a fourth
// rule ("zlib1g") gets a result whose message has no purl at all. Their
// message-label layout is invented, not observed; only the four real results
// should be read as a fact about Scout's output.
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
	if len(findings) != 7 {
		t.Fatalf("got %d findings, want 7", len(findings))
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
//
// Both packages must actually turn up before their FixedIn is asserted on: a
// mutant that deletes the message-parsing block entirely reads Package from
// the rule's first purl for every result referencing it, so both results
// here would report Package "nghttp2" and neither would report "nginx" — an
// unchecked zero-value Finding{} for "nginx" has FixedIn == "", which passes
// the empty-FixedIn assertion below for the wrong reason. found catches that.
func TestParseFindingsTakesFixedVersionPerResult(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var nghttp2, nginx Finding
	var foundNghttp2, foundNginx bool
	for _, f := range findings {
		switch f.Package {
		case "nghttp2":
			nghttp2, foundNghttp2 = f, true
		case "nginx":
			nginx, foundNginx = f, true
		}
	}
	if !foundNghttp2 {
		t.Fatal("no finding has Package \"nghttp2\" — the message-derived package split did not happen")
	}
	if !foundNginx {
		t.Fatal("no finding has Package \"nginx\" — the message-derived package split did not happen")
	}
	if nghttp2.FixedIn != "1.43.0-1+deb11u1" {
		t.Errorf("nghttp2 FixedIn = %q", nghttp2.FixedIn)
	}
	// "not fixed" is prose, not a version. It must not reach the page as one.
	if nginx.FixedIn != "" {
		t.Errorf("nginx FixedIn = %q, want empty for an unfixed package", nginx.FixedIn)
	}
}

// A message can name a package without saying whether it's fixed — Scout
// omits the "Fixed version" line rather than always printing one. The
// libssl1.1 result here references the nghttp2/nginx rule (CVE-2023-44487)
// but names a package that rule never declared, so its rule.fixed_version
// (nghttp2's fix) belongs to neither nghttp2 nor nginx nor libssl1.1 for this
// result. If the rule's value survived because the message didn't repeat it,
// libssl1.1 would print nghttp2's fix — a fix it has no connection to at
// all, which is the sharpest version of the harm this task exists to
// prevent: once the message names a package, the rule's fields must be
// abandoned wholesale, not kept for whichever field the message left blank.
func TestParseFindingsMessagePurlWithoutFixedVersionYieldsEmptyFixedIn(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Package == "libssl1.1" {
			found = true
			if f.Installed != "1.1.1n-0+deb11u4" {
				t.Errorf("Installed = %q", f.Installed)
			}
			if f.FixedIn != "" {
				t.Errorf("FixedIn = %q, want empty: the message named the package but had no "+
					"Fixed version line, so the rule's fixed_version (nghttp2's fix) must not "+
					"leak through", f.FixedIn)
			}
		}
	}
	if !found {
		t.Fatal("no finding has Package \"libssl1.1\" — the message-derived package split did not happen")
	}
}

// A message with no purl at all — no "Package" line Scout could print — falls
// back to the rule for every field, not just the ones a partial message left
// unset. This is the fallback path itself, distinct from the tests above
// that exercise a message overriding it.
func TestParseFindingsMessageWithoutPurlFallsBackToRule(t *testing.T) {
	findings, err := Scout{}.ParseFindings(realSarif(t))
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Package == "zlib1g" {
			found = true
			if f.Installed != "1.2.11.dfsg-2+deb11u1" {
				t.Errorf("Installed = %q", f.Installed)
			}
			if f.FixedIn != "1.2.11.dfsg-2+deb11u2" {
				t.Errorf("FixedIn = %q", f.FixedIn)
			}
		}
	}
	if !found {
		t.Fatal("no finding has Package \"zlib1g\" — the rule fallback did not run")
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
		"CRITICAL": 1, "HIGH": 1, "MEDIUM": 1, "LOW": 3, "UNSPECIFIED": 1,
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

// trivyJSON is a synthetic (not captured) fixture in Trivy's native --format
// json shape: one Results entry with three vulnerabilities (a fixed CRITICAL,
// an unfixed HIGH, and an UNKNOWN-severity one Trivy's own vocabulary uses but
// kx's canonical Severities does not), and a second Results entry with no
// Vulnerabilities at all — a clean scan target contributes nothing.
const trivyJSON = `{
  "Results": [
    {
      "Target": "app (debian 12.5)",
      "Vulnerabilities": [
        {
          "VulnerabilityID": "CVE-2024-0001",
          "PkgName": "openssl",
          "InstalledVersion": "3.0.11-1",
          "FixedVersion": "3.0.13-1",
          "Severity": "CRITICAL",
          "PrimaryURL": "https://avd.aquasec.com/nvd/cve-2024-0001"
        },
        {
          "VulnerabilityID": "CVE-2024-0002",
          "PkgName": "libcurl4",
          "InstalledVersion": "7.88.1-10",
          "FixedVersion": "",
          "Severity": "HIGH",
          "PrimaryURL": "https://avd.aquasec.com/nvd/cve-2024-0002"
        },
        {
          "VulnerabilityID": "CVE-2024-0003",
          "PkgName": "zlib1g",
          "InstalledVersion": "1.2.13.dfsg-1",
          "FixedVersion": "1.2.13.dfsg-1+deb12u1",
          "Severity": "UNKNOWN",
          "PrimaryURL": "https://avd.aquasec.com/nvd/cve-2024-0003"
        }
      ]
    },
    {
      "Target": "app (gobinary)",
      "Vulnerabilities": null
    }
  ]
}`

func TestTrivyParseFindingsReadsEveryVulnerability(t *testing.T) {
	findings, err := Trivy{}.ParseFindings(trivyJSON)
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3", len(findings))
	}

	first := findings[0]
	if first.ID != "CVE-2024-0001" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Severity != "CRITICAL" {
		t.Errorf("Severity = %q", first.Severity)
	}
	if first.Package != "openssl" {
		t.Errorf("Package = %q", first.Package)
	}
	if first.Installed != "3.0.11-1" {
		t.Errorf("Installed = %q", first.Installed)
	}
	if first.FixedIn != "3.0.13-1" {
		t.Errorf("FixedIn = %q", first.FixedIn)
	}
	if first.URL != "https://avd.aquasec.com/nvd/cve-2024-0001" {
		t.Errorf("URL = %q", first.URL)
	}
}

func TestTrivyParseFindingsEmptyFixedVersionStaysEmpty(t *testing.T) {
	findings, err := Trivy{}.ParseFindings(trivyJSON)
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Package == "libcurl4" {
			found = true
			if f.FixedIn != "" {
				t.Errorf("FixedIn = %q, want empty for an unfixed package", f.FixedIn)
			}
		}
	}
	if !found {
		t.Fatal("no finding has Package \"libcurl4\"")
	}
}

func TestTrivyParseFindingsFoldsUnknownSeverityToUnspecified(t *testing.T) {
	findings, err := Trivy{}.ParseFindings(trivyJSON)
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	var found bool
	for _, f := range findings {
		if f.Package == "zlib1g" {
			found = true
			if f.Severity != "UNSPECIFIED" {
				t.Errorf("Severity = %q, want UNSPECIFIED for Trivy's own UNKNOWN", f.Severity)
			}
		}
	}
	if !found {
		t.Fatal("no finding has Package \"zlib1g\"")
	}
}

func TestTrivyParseFindingsSkipsCleanTargets(t *testing.T) {
	findings, err := Trivy{}.ParseFindings(trivyJSON)
	if err != nil {
		t.Fatalf("ParseFindings returned %v", err)
	}
	for _, f := range findings {
		if strings.Contains(f.ID, "gobinary") || strings.Contains(f.Package, "gobinary") {
			t.Errorf("a finding leaked from the Vulnerabilities:null target: %+v", f)
		}
	}
}

func TestTrivyParseFindingsRejectsGarbage(t *testing.T) {
	if _, err := (Trivy{}).ParseFindings("not json"); err == nil {
		t.Error("ParseFindings accepted non-JSON")
	}
}

func TestTrivyArgv(t *testing.T) {
	trivy := Trivy{}
	got := trivy.SummaryArgv("nginx:1.25")
	if len(got) != 5 || got[2] != "--format" || got[3] != "json" || got[4] != "nginx:1.25" {
		t.Errorf("SummaryArgv = %v, want a json format request", got)
	}
	passthrough := trivy.PassthroughArgv("nginx:1.25", []string{"--severity", "HIGH"})
	if passthrough[len(passthrough)-1] != "HIGH" {
		t.Errorf("PassthroughArgv = %v, want the extra flag forwarded", passthrough)
	}
	if preflight := trivy.PreflightArgv(); preflight[0] != "trivy" {
		t.Errorf("PreflightArgv = %v", preflight)
	}
	if trivy.Name() != "trivy" {
		t.Errorf("Name() = %q", trivy.Name())
	}
	if !strings.Contains(trivy.UnavailableMessage(), "trivy.dev") {
		t.Errorf("UnavailableMessage() = %q, want it to link install docs", trivy.UnavailableMessage())
	}
}

func TestGetEngineKnowsTrivy(t *testing.T) {
	if _, err := GetEngine("trivy"); err != nil {
		t.Errorf("GetEngine(trivy): %v", err)
	}
}

func TestNamesAndExists(t *testing.T) {
	names := Names()
	if len(names) != 2 || names[0] != "scout" || names[1] != "trivy" {
		t.Errorf("Names() = %v, want [scout trivy] in that order", names)
	}
	if !Exists("scout") || !Exists("trivy") {
		t.Error("Exists false for a registered engine")
	}
	if Exists("nonexistent") {
		t.Error("Exists true for an unregistered engine")
	}
}
