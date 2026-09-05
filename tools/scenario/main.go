// Command scenario applies and removes the cluster fixtures under
// tests/scenarios, so a situation kx is supposed to diagnose can be created
// on demand rather than described in a comment.
//
// Each scenario is a directory holding a manifest, an optional set of status
// patches, and a README saying what it proves. The two-file split is not
// stylistic: `kubectl apply` silently drops a status block, so anything a
// controller would normally write — a container's termination, a Job's
// failure — has to be patched onto the object after it exists.
//
// Timestamps in those patches are written relative to the run: "@-46d" is
// expanded to an RFC 3339 time 46 days before now, in the same vocabulary
// kx diag --since reads. A committed fixture with an absolute date would
// age into meaninglessness.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jzills/kx/internal/config"
)

// scenariosDir is where the fixtures live, relative to the repository root.
const scenariosDir = "tests/scenarios"

// namespacePrefix keeps a scenario's objects out of every other namespace,
// the demo namespace included: a scenario is applied and deleted whole, and
// nothing it does should disturb a cluster someone is also demoing on.
const namespacePrefix = "kx-"

// patch is one status patch: which object it targets, and the status to
// merge onto it.
type patch struct {
	Kind  string         `yaml:"kind"`
	Name  string         `yaml:"name"`
	Patch map[string]any `yaml:"patch"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch command := os.Args[1]; command {
	case "list":
		err = list()
	case "apply", "delete":
		if len(os.Args) < 3 {
			err = fmt.Errorf("%s needs a scenario name; run `scenario list`", command)
			break
		}
		if command == "apply" {
			err = apply(os.Args[2])
		} else {
			err = remove(os.Args[2])
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scenario:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/scenario <list|apply|delete> [name]")
}

func list() error {
	names, err := scenarios()
	if err != nil {
		return err
	}
	for _, name := range names {
		fmt.Printf("%-20s %s\n", name, summary(name))
	}
	return nil
}

func scenarios() ([]string, error) {
	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// summary is the scenario's one-line description, taken from the first
// heading of its README so the listing cannot drift from the document
// someone actually reads.
func summary(name string) string {
	readme, err := os.ReadFile(filepath.Join(scenariosDir, name, "README.md"))
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(readme), "\n")
	title := strings.TrimSpace(strings.TrimPrefix(first, "#"))
	if _, description, found := strings.Cut(title, "—"); found {
		return strings.TrimSpace(description)
	}
	return title
}

func apply(name string) error {
	dir := filepath.Join(scenariosDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("no scenario %q; run `scenario list`", name)
	}

	// Piped rather than applied by path: an Event carries its own timestamps
	// in the object, so the manifest needs the same "@-21d" expansion the
	// status patches get.
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return err
	}
	expanded, err := expandTimestamps(string(manifest), time.Now())
	if err != nil {
		return err
	}
	if err := kubectlWithInput(expanded, "apply", "-f", "-"); err != nil {
		return err
	}

	patches, err := statusPatches(dir)
	if err != nil {
		return err
	}

	namespace := namespacePrefix + name
	for _, p := range patches {
		body, err := json.Marshal(map[string]any{"status": p.Patch})
		if err != nil {
			return err
		}
		// The status subresource, not the object: the API server ignores a
		// status written through a normal apply or patch.
		if err := kubectl("patch", strings.ToLower(p.Kind), p.Name,
			"-n", namespace, "--subresource=status", "--type=merge",
			"-p", string(body)); err != nil {
			return err
		}
	}

	fmt.Printf("\nApplied %s to namespace %s\n", name, namespace)
	fmt.Printf("Read %s/README.md for what to run against it.\n", dir)
	return nil
}

// remove deletes the namespace rather than the manifest, so an object added
// to a scenario by hand mid-session goes with it.
func remove(name string) error {
	return kubectl("delete", "namespace", namespacePrefix+name, "--ignore-not-found")
}

func statusPatches(dir string) ([]patch, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "status.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	expanded, err := expandTimestamps(string(raw), time.Now())
	if err != nil {
		return nil, err
	}
	var patches []patch
	if err := yaml.Unmarshal([]byte(expanded), &patches); err != nil {
		return nil, err
	}
	return patches, nil
}

// relativeTime matches the "@-46d" placeholders a status patch dates itself
// with.
var relativeTime = regexp.MustCompile(`@-([0-9a-z.]+)`)

// expandTimestamps rewrites those placeholders to absolute RFC 3339 times.
//
// The offset is parsed by config.ParseDuration, the same reader behind
// kx diag --since, so a fixture and the flag that filters it cannot disagree
// about what "46d" means.
func expandTimestamps(document string, now time.Time) (string, error) {
	var failure error
	expanded := relativeTime.ReplaceAllStringFunc(document, func(match string) string {
		offset, err := config.ParseDuration(strings.TrimPrefix(match, "@-"))
		if err != nil {
			failure = fmt.Errorf("%s: %w", match, err)
			return match
		}
		return now.Add(-offset).UTC().Format(time.RFC3339)
	})
	return expanded, failure
}

func kubectl(args ...string) error { return kubectlWithInput("", args...) }

func kubectlWithInput(stdin string, args ...string) error {
	cmd := exec.Command("kubectl", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return nil
}
