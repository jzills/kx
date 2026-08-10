// Package scanner runs a container image vulnerability scanner and parses its
// output into per-vulnerability findings, which are then rolled up into
// severity counts.
//
// This is the one place kx shells out to something other than kubectl, so the
// scanner is behind an Engine interface: adding a second one is a matter of
// describing its argv and how to read its output, not of touching the command.
package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// Severities are the canonical buckets, most severe first. Scout's SARIF uses
// exactly these labels in rule.properties.cvssV3_severity.
var Severities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNSPECIFIED"}

// ImageScan is the rolled-up result for one image. Counts is nil when the scan
// itself failed — an unpullable image, an auth problem, unparseable output —
// and Error carries a short reason for the table's status cell. Findings is
// the detail behind Counts — one entry per SARIF result — for anything that
// needs more than the tally.
type ImageScan struct {
	Image    string
	Counts   map[string]int
	Findings []Finding
	Error    string
}

// Service runs scanner commands.
type Service interface {
	// Scan streams the scanner's own output to the terminal and returns its
	// exit code.
	Scan(argv []string) (int, error)
	// Capture collects stdout for structured parsing.
	Capture(argv []string) (stdout, stderr string, code int, err error)
	// Probe runs silently and reports only the exit code.
	Probe(argv []string) (int, error)
}

// ExecService is the real service, shelling out to the scanner binary.
type ExecService struct{}

func missingBinary(argv []string) error {
	return fmt.Errorf("%s not found on PATH — install it to run this scan.", argv[0])
}

func (ExecService) Scan(argv []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	// Inherit stdio so the scanner streams straight to the terminal.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return 1, missingBinary(argv)
	}
	if err != nil {
		// A failure that is not an exit status has no exit code to report;
		// answering 0 alongside an error reads as success to anything that
		// checks the code first, as Capture and Probe already avoid doing.
		return 1, err
	}
	return 0, nil
}

func (ExecService) Capture(argv []string) (string, string, int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", "", 1, missingBinary(argv)
	}
	if err != nil {
		return "", "", 1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

func (ExecService) Probe(argv []string) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return 1, missingBinary(argv)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

// Engine describes one scanner: how to check it's usable, how to invoke it, and
// how to read what it returns.
type Engine interface {
	Name() string
	// PreflightArgv is a cheap command that exits 0 only when the scanner is
	// usable.
	PreflightArgv() []string
	// UnavailableMessage is shown when the preflight fails.
	UnavailableMessage() string
	// PassthroughArgv streams the scanner's own report for an image.
	PassthroughArgv(image string, extra []string) []string
	// SummaryArgv asks for machine-readable output.
	SummaryArgv(image string) []string
	// ParseFindings reads that output into one Finding per result.
	ParseFindings(stdout string) ([]Finding, error)
}

const scoutDocsURL = "https://docs.docker.com/scout/"

// Scout is the Docker Scout engine.
type Scout struct{}

func (Scout) Name() string { return "scout" }

// PreflightArgv checks the plugin is installed: `docker scout` is an optional
// CLI plugin, and a plain Docker install answers `docker scout version` with
// "unknown command" and exits 1.
func (Scout) PreflightArgv() []string { return []string{"docker", "scout", "version"} }

func (Scout) UnavailableMessage() string {
	return "docker scout is not available — kx scan needs the Docker Scout " +
		"CLI plugin. Install it: " + scoutDocsURL
}

func (Scout) PassthroughArgv(image string, extra []string) []string {
	return append([]string{"docker", "scout", "cves", image}, extra...)
}

func (Scout) SummaryArgv(image string) []string {
	return []string{"docker", "scout", "cves", "--format", "sarif", image}
}

// Finding is one vulnerability in one package. Fields Scout does not supply
// stay empty and their columns are dropped rather than invented.
type Finding struct {
	ID        string
	Severity  string
	Package   string
	Installed string
	FixedIn   string
	URL       string
}

// CountBySeverity tallies findings into the canonical buckets.
//
// The counts the summary table prints are derived from the findings rather
// than parsed separately, so the table and the HTML CVE list cannot disagree
// about which bucket anything landed in.
//
// A Severity outside the canonical list counts as UNSPECIFIED rather than
// being dropped: ParseFindings never produces one today, but this is exported
// beside a multi-engine Engine interface, and a future engine's own severity
// vocabulary should lose a label to the catch-all, not out of the total.
func CountBySeverity(findings []Finding) map[string]int {
	known := make(map[string]bool, len(Severities))
	counts := make(map[string]int, len(Severities))
	for _, severity := range Severities {
		known[severity] = true
		counts[severity] = 0
	}
	for _, finding := range findings {
		severity := finding.Severity
		if !known[severity] {
			severity = "UNSPECIFIED"
		}
		counts[severity]++
	}
	return counts
}

// sarifDocument is the slice of SARIF Scout populates.
type sarifDocument struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Rules []struct {
					ID         string `json:"id"`
					HelpURI    string `json:"helpUri"`
					Properties struct {
						Severity     string   `json:"cvssV3_severity"`
						FixedVersion string   `json:"fixed_version"`
						Purls        []string `json:"purls"`
					} `json:"properties"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID    string `json:"ruleId"`
			RuleIndex *int   `json:"ruleIndex"`
			Message   struct {
				Text string `json:"text"`
			} `json:"message"`
		} `json:"results"`
	} `json:"runs"`
}

// ParseFindings reads SARIF into one Finding per result.
//
// Per result, not per rule: a rule covering two packages emits two results,
// and one finding per rule would lose one of them — and with it the count,
// since the severity tallies have always been over results.
//
// Package and fixed version are read from the result's own message, which is
// the only per-result source for them. A rule's fixed_version describes just
// one of its packages: CVE-2023-44487 is fixed for nghttp2 and unfixed for
// nginx, and the rule carries only the former. The rule's values remain as
// fallbacks for a message that does not parse.
func (Scout) ParseFindings(stdout string) ([]Finding, error) {
	var document sarifDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		return nil, err
	}

	known := map[string]bool{}
	for _, severity := range Severities {
		known[severity] = true
	}

	var findings []Finding
	for _, run := range document.Runs {
		rules := run.Tool.Driver.Rules
		for _, result := range run.Results {
			finding := Finding{ID: result.RuleID, Severity: "UNSPECIFIED"}

			if result.RuleIndex != nil && *result.RuleIndex >= 0 && *result.RuleIndex < len(rules) {
				rule := rules[*result.RuleIndex]
				if value := strings.ToUpper(rule.Properties.Severity); known[value] {
					finding.Severity = value
				}
				finding.URL = rule.HelpURI
				if finding.ID == "" {
					finding.ID = rule.ID
				}
				finding.FixedIn = fixedVersion(rule.Properties.FixedVersion)
				if len(rule.Properties.Purls) > 0 {
					finding.Package, finding.Installed = parsePurl(rule.Properties.Purls[0])
				}
			}

			// The message is per-result and therefore authoritative. Once it
			// yields a purl, the rule's values are abandoned wholesale rather
			// than field by field: a rule's fixed_version describes only one
			// of its packages, so keeping it beside a package the message
			// named is exactly how a fix gets promised for a package that has
			// none.
			if purl := purlPattern.FindString(result.Message.Text); purl != "" {
				finding.Package, finding.Installed = parsePurl(purl)
				finding.FixedIn = ""
				if fixed, ok := messageField(result.Message.Text, "Fixed version"); ok {
					finding.FixedIn = fixedVersion(fixed)
				}
			}

			findings = append(findings, finding)
		}
	}
	return findings, nil
}

const trivyDocsURL = "https://trivy.dev/"

// Trivy is the Aqua Security Trivy engine.
type Trivy struct{}

func (Trivy) Name() string { return "trivy" }

// PreflightArgv checks the trivy binary is installed and runnable.
func (Trivy) PreflightArgv() []string { return []string{"trivy", "--version"} }

func (Trivy) UnavailableMessage() string {
	return "trivy is not available — kx scan needs the Trivy CLI. " +
		"Install it: " + trivyDocsURL
}

func (Trivy) PassthroughArgv(image string, extra []string) []string {
	return append([]string{"trivy", "image", image}, extra...)
}

func (Trivy) SummaryArgv(image string) []string {
	return []string{"trivy", "image", "--format", "json", image}
}

// trivyDocument is the shape of Trivy's native `--format json` output.
type trivyDocument struct {
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			PrimaryURL       string `json:"PrimaryURL"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// ParseFindings reads Trivy's native JSON into one Finding per vulnerability.
//
// Unlike Scout's SARIF, Trivy's JSON already carries package, version and
// severity as named fields per vulnerability, so there is no purl parsing or
// per-result message scraping to do.
func (Trivy) ParseFindings(stdout string) ([]Finding, error) {
	var document trivyDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		return nil, err
	}

	known := map[string]bool{}
	for _, severity := range Severities {
		known[severity] = true
	}

	var findings []Finding
	for _, result := range document.Results {
		for _, vulnerability := range result.Vulnerabilities {
			severity := strings.ToUpper(vulnerability.Severity)
			if !known[severity] {
				severity = "UNSPECIFIED"
			}
			findings = append(findings, Finding{
				ID:        vulnerability.VulnerabilityID,
				Severity:  severity,
				Package:   vulnerability.PkgName,
				Installed: vulnerability.InstalledVersion,
				FixedIn:   vulnerability.FixedVersion,
				URL:       vulnerability.PrimaryURL,
			})
		}
	}
	return findings, nil
}

// purlPattern matches the package URL embedded in a result's message.
//
// Matched rather than parsed off its label, so a change to Scout's column
// padding or wording cannot break it.
var purlPattern = regexp.MustCompile(`pkg:\S+`)

// messageField reads one "Label :value" line out of a result's message. The
// value is everything after the first colon, so a Debian epoch version like
// "1:2.3-4" survives intact.
func messageField(text, label string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		name, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(name) == label {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// fixedVersion normalises Scout's "not fixed", which is prose rather than a
// version and must not be printed as one.
func fixedVersion(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "not fixed") {
		return ""
	}
	return strings.TrimSpace(value)
}

// osPackageTypes are the purl types whose namespace is a distribution rather
// than part of the package's identity.
var osPackageTypes = map[string]bool{"deb": true, "rpm": true, "apk": true}

// parsePurl reads a package URL into a display name and the installed version.
//
// "pkg:deb/debian/curl@7.74.0-1.3%2Bdeb11u1?os_distro=bullseye" is curl at
// 7.74.0-1.3+deb11u1: the distro namespace is noise. A Go module keeps its
// whole path, because "golang.org/x/crypto" collapsed to "crypto" names
// nothing.
func parsePurl(purl string) (name, version string) {
	body := strings.TrimPrefix(purl, "pkg:")
	if cut := strings.IndexAny(body, "?#"); cut >= 0 {
		body = body[:cut]
	}
	if at := strings.LastIndex(body, "@"); at >= 0 {
		version = decodePurl(body[at+1:])
		body = body[:at]
	}

	segments := strings.Split(body, "/")
	if len(segments) < 2 {
		return decodePurl(body), version
	}
	if osPackageTypes[segments[0]] {
		return decodePurl(segments[len(segments)-1]), version
	}
	return decodePurl(strings.Join(segments[1:], "/")), version
}

// decodePurl undoes the percent-encoding purls carry; a Debian version's "+"
// arrives as %2B.
func decodePurl(value string) string {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}

var engines = map[string]Engine{"scout": Scout{}, "trivy": Trivy{}}

// engineOrder is `kx engine`'s display order — registration order, not
// alphabetical, so Scout (the default) is always listed first.
var engineOrder = []string{"scout", "trivy"}

// Names returns the registered engine names in display order.
func Names() []string {
	names := make([]string, len(engineOrder))
	copy(names, engineOrder)
	return names
}

// Exists reports whether name is a registered engine.
func Exists(name string) bool {
	_, ok := engines[name]
	return ok
}

// GetEngine resolves an engine by name.
func GetEngine(name string) (Engine, error) {
	engine, ok := engines[name]
	if !ok {
		names := make([]string, 0, len(engines))
		for known := range engines {
			names = append(names, known)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown engine '%s'. Available engines: %s.",
			name, strings.Join(names, ", "))
	}
	return engine, nil
}
