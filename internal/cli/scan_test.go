package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/scanner"
)

func decode(t *testing.T, document string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(document), &object); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return object
}

// The PodSpec sits at a different depth for each workload kind, which is the
// whole difficulty of resolving images.
func TestPodSpecLocation(t *testing.T) {
	cases := map[string]struct {
		document string
		want     []string
	}{
		"bare pod": {
			`{"kind":"Pod","spec":{"containers":[{"image":"nginx:1.25"}]}}`,
			[]string{"nginx:1.25"},
		},
		"deployment": {
			`{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"image":"api:v2"}]}}}}`,
			[]string{"api:v2"},
		},
		"cronjob": {
			`{"kind":"CronJob","spec":{"jobTemplate":{"spec":{"template":{"spec":{
			   "containers":[{"image":"batch:v1"}]}}}}}}`,
			[]string{"batch:v1"},
		},
		"init containers first": {
			`{"kind":"Pod","spec":{"initContainers":[{"image":"migrate:v1"}],
			   "containers":[{"image":"app:v1"}]}}`,
			[]string{"migrate:v1", "app:v1"},
		},
		"no containers": {`{"kind":"Pod","spec":{}}`, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := imagesOf(decode(t, tc.document))
			if len(got) != len(tc.want) {
				t.Fatalf("images = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("images[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A workload's replicas share an image; scanning it once is the point.
func TestDedupePreservesFirstSeenOrder(t *testing.T) {
	got := dedupe([]string{"b", "a", "b", "c", "a"})
	if strings.Join(got, ",") != "b,a,c" {
		t.Errorf("dedupe = %v, want [b a c]", got)
	}
}

// fakeScanner records invocations and replays scripted results.
type fakeScanner struct {
	probeCode int
	captures  []struct {
		stdout, stderr string
		code           int
	}
	calls int
}

func (f *fakeScanner) Scan([]string) (int, error) { return 0, nil }
func (f *fakeScanner) Probe([]string) (int, error) {
	return f.probeCode, nil
}
func (f *fakeScanner) Capture([]string) (string, string, int, error) {
	if f.calls < len(f.captures) {
		result := f.captures[f.calls]
		f.calls++
		return result.stdout, result.stderr, result.code, nil
	}
	f.calls++
	return "", "", 1, nil
}

// A missing scanner is reported once with a fix, not once per image.
func TestEnsureAvailableReportsMissingScanner(t *testing.T) {
	command := ScanCommand{Scanner: &fakeScanner{probeCode: 1}, Status: noStatus}
	_, err := command.EnsureAvailable("scout")
	if err == nil {
		t.Fatal("EnsureAvailable succeeded with the scanner missing")
	}
	if !strings.Contains(err.Error(), "docs.docker.com/scout") {
		t.Errorf("error = %q, want it to link the install docs", err)
	}
}

func TestEnsureAvailableRejectsUnknownEngine(t *testing.T) {
	command := ScanCommand{Scanner: &fakeScanner{}, Status: noStatus}
	if _, err := command.EnsureAvailable("nonexistent"); err == nil {
		t.Error("EnsureAvailable accepted an unknown engine")
	}
}

// One unpullable image records its failure on that row rather than costing the
// results for every other image.
func TestSummarizeRecordsPerImageFailures(t *testing.T) {
	scanner := &fakeScanner{captures: []struct {
		stdout, stderr string
		code           int
	}{
		{stdout: `{"runs":[]}`},
		{stderr: "progress noise\nError: failed to pull image", code: 1},
		{stdout: "not json"},
	}}

	rows, err := ScanCommand{Scanner: scanner, Status: noStatus}.
		Summarize("scout", []string{"good:v1", "unpullable:v1", "weird:v1"})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Counts == nil {
		t.Error("the successful scan has no counts")
	}
	// The last stderr line is the specific one; earlier lines are progress.
	if rows[1].Error != "Error: failed to pull image" {
		t.Errorf("error = %q, want the last stderr line", rows[1].Error)
	}
	if rows[2].Error != "unparseable output" {
		t.Errorf("error = %q, want an unparseable-output note", rows[2].Error)
	}
}

func TestLastLineFallsBackWhenStderrIsEmpty(t *testing.T) {
	if got := lastLine("   \n\n"); got != "scan failed" {
		t.Errorf("lastLine = %q, want a fallback reason", got)
	}
}

func TestImagesNoun(t *testing.T) {
	if got := imagesNoun(1); got != "1 image" {
		t.Errorf("imagesNoun(1) = %q", got)
	}
	if got := imagesNoun(3); got != "3 images" {
		t.Errorf("imagesNoun(3) = %q", got)
	}
}

var _ scanner.Service = (*fakeScanner)(nil)

// `kx scan web` is a mistyped index, not a scanner flag. Sweeping the whole
// namespace instead would act on something other than what was typed, and
// outside --full the stray argument is dropped without a word.
//
// The guard returns before any cluster or scanner call, which is what makes it
// testable without either.
func TestScanRejectsANonNumericIndex(t *testing.T) {
	quietRender(t)
	cmd := newScanCommand(Services{})
	err := cmd.RunE(cmd, []string{"web"})
	if err == nil {
		t.Fatal("a non-numeric index was accepted")
	}
	if !strings.Contains(err.Error(), "'web' is not a valid int") {
		t.Errorf("err = %v, want it to name the bad argument", err)
	}
}
