// Package scanner runs a container image vulnerability scanner and rolls its
// output up into severity counts.
//
// This is the one place kx shells out to something other than kubectl, so the
// scanner is behind an Engine interface: adding a second one is a matter of
// describing its argv and how to read its output, not of touching the command.
package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Severities are the canonical buckets, most severe first. Scout's SARIF uses
// exactly these labels in rule.properties.cvssV3_severity.
var Severities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNSPECIFIED"}

// ImageScan is the rolled-up result for one image. Counts is nil when the scan
// itself failed — an unpullable image, an auth problem, unparseable output —
// and Error carries a short reason for the table's status cell.
type ImageScan struct {
	Image  string
	Counts map[string]int
	Error  string
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
	return 0, err
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
	// ParseCounts rolls that output up into severity counts.
	ParseCounts(stdout string) (map[string]int, error)
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

// ParseCounts reads SARIF, mapping each result to its rule's severity. A result
// whose rule index is missing or out of range counts as UNSPECIFIED rather than
// being dropped, so the totals always account for every finding.
func (Scout) ParseCounts(stdout string) (map[string]int, error) {
	var document struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						Properties struct {
							Severity string `json:"cvssV3_severity"`
						} `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleIndex *int `json:"ruleIndex"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		return nil, err
	}

	known := map[string]bool{}
	counts := map[string]int{}
	for _, severity := range Severities {
		counts[severity] = 0
		known[severity] = true
	}

	for _, run := range document.Runs {
		rules := run.Tool.Driver.Rules
		for _, result := range run.Results {
			severity := "UNSPECIFIED"
			if result.RuleIndex != nil && *result.RuleIndex >= 0 && *result.RuleIndex < len(rules) {
				if value := rules[*result.RuleIndex].Properties.Severity; value != "" {
					severity = strings.ToUpper(value)
				}
			}
			if !known[severity] {
				severity = "UNSPECIFIED"
			}
			counts[severity]++
		}
	}
	return counts, nil
}

var engines = map[string]Engine{"scout": Scout{}}

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
