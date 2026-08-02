package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/web"
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

// The selector, the banner label and the empty-result message all describe the
// same scope and have to agree; they live on one type for that reason.
func TestScanScopeDescribesOneNamespace(t *testing.T) {
	scope := scanScope{Namespace: "prod"}
	if got := strings.Join(scope.selector(), " "); got != "-n prod" {
		t.Errorf("selector = %q, want %q", got, "-n prod")
	}
	if got := scope.label(); got != "prod" {
		t.Errorf("label = %q, want the namespace", got)
	}
	if got := scope.emptyMessage(); !strings.Contains(got, "'prod'") {
		t.Errorf("emptyMessage = %q, want it to name the namespace", got)
	}
}

func TestScanScopeDescribesAllNamespaces(t *testing.T) {
	scope := scanScope{All: true}
	if got := strings.Join(scope.selector(), " "); got != "--all-namespaces" {
		t.Errorf("selector = %q, want %q", got, "--all-namespaces")
	}
	// The literal string kx get -A already prints for the same scope.
	if got := scope.label(); got != "all namespaces" {
		t.Errorf("label = %q, want %q", got, "all namespaces")
	}
	if got := scope.emptyMessage(); !strings.Contains(got, "any namespace") {
		t.Errorf("emptyMessage = %q, want it to cover every namespace", got)
	}
}

// The regression test for the actual bug: `kx scan -n prod` used to sweep the
// current namespace and report it as though it were prod.
func TestCollectSweepsTheNamespaceItIsGiven(t *testing.T) {
	kubectl := &fakeKubectl{
		namespace: "kube-system",
		output:    `{"items":[{"kind":"Pod","spec":{"containers":[{"image":"api:v1"}]}}]}`,
	}
	command := ScanCommand{Kubectl: kubectl, Scanner: &fakeScanner{}, Status: noStatus}

	images, err := command.Collect(scanScope{Namespace: "prod"}, "scout")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if strings.Join(images, ",") != "api:v1" {
		t.Errorf("images = %v, want [api:v1]", images)
	}
	argv := strings.Join(kubectl.args, " ")
	if !strings.Contains(argv, "-n prod") {
		t.Errorf("argv = %q, want it to sweep 'prod'", argv)
	}
	if strings.Contains(argv, "kube-system") {
		t.Errorf("argv = %q, swept the current namespace instead", argv)
	}
}

func TestCollectAllNamespacesUsesTheClusterWideSelector(t *testing.T) {
	kubectl := &fakeKubectl{
		output: `{"items":[{"kind":"Pod","spec":{"containers":[{"image":"api:v1"}]}}]}`,
	}
	command := ScanCommand{Kubectl: kubectl, Scanner: &fakeScanner{}, Status: noStatus}

	if _, err := command.Collect(scanScope{All: true}, "scout"); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	argv := strings.Join(kubectl.args, " ")
	if !strings.Contains(argv, "--all-namespaces") {
		t.Errorf("argv = %q, want the cluster-wide selector", argv)
	}
	if strings.Contains(argv, "-n ") {
		t.Errorf("argv = %q, want no single-namespace selector", argv)
	}
}

// An empty sweep names the scope it searched, so the message is actionable.
func TestCollectReportsAnEmptyScope(t *testing.T) {
	kubectl := &fakeKubectl{output: `{"items":[]}`}
	command := ScanCommand{Kubectl: kubectl, Scanner: &fakeScanner{}, Status: noStatus}

	_, err := command.Collect(scanScope{All: true}, "scout")
	if err == nil {
		t.Fatal("an empty sweep succeeded")
	}
	if !strings.Contains(err.Error(), "any namespace") {
		t.Errorf("err = %v, want it to name the scope", err)
	}
}

// An index resolves a name from one namespace's listing; scanning that name
// somewhere else finds a different resource or nothing at all.
func TestScanRejectsANamespaceFlagAlongsideAnIndex(t *testing.T) {
	for _, argv := range [][]string{
		{"1", "-n", "prod"},
		{"1", "--namespace=prod"},
		{"1", "-A"},
		{"1", "--all-namespaces"},
	} {
		quietRender(t)
		cmd := newScanCommand(Services{})
		err := cmd.RunE(cmd, argv)
		if err == nil {
			t.Fatalf("kx scan %v was accepted", argv)
		}
		if !strings.Contains(err.Error(), "cannot be combined with an index") {
			t.Errorf("kx scan %v: err = %v", argv, err)
		}
	}
}

// `-n ""` is still a namespace flag for the purpose of the guards, which is the
// distinction hasFlag exists to preserve.
func TestScanRejectsAnEmptyNamespaceFlagAlongsideAnIndex(t *testing.T) {
	quietRender(t)
	cmd := newScanCommand(Services{})
	err := cmd.RunE(cmd, []string{"1", "-n", ""})
	if err == nil {
		t.Fatal("kx scan 1 -n \"\" was accepted")
	}
	if !strings.Contains(err.Error(), "cannot be combined with an index") {
		t.Errorf("err = %v", err)
	}
}

func TestScanRejectsNamespaceAndAllNamespacesTogether(t *testing.T) {
	quietRender(t)
	cmd := newScanCommand(Services{})
	err := cmd.RunE(cmd, []string{"-n", "prod", "-A"})
	if err == nil {
		t.Fatal("-n and -A were accepted together")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("err = %v", err)
	}
}

// scanPage is the only place a ScanPage's fields are set, so a direct test
// pins each one to its source without needing a live cluster or a served
// page — swapping Scope for something else, or dropping a row, fails this
// directly rather than only showing up once a page is rendered.
func TestScanPageMapsScopeAndImagesFromTheSummary(t *testing.T) {
	rows := []scanner.ImageScan{
		{Image: "api:v1", Counts: map[string]int{"critical": 1}},
	}
	page := scanPage("prod", rows, web.Meta{Title: "t"})

	if page.Scope != "prod" {
		t.Errorf("Scope = %q, want prod", page.Scope)
	}
	if len(page.Images) != 1 || page.Images[0].Image != "api:v1" {
		t.Errorf("Images = %+v, want the one api:v1 row", page.Images)
	}
	if page.Meta.Title != "t" {
		t.Errorf("Meta = %+v, want the meta passed in carried through unchanged", page.Meta)
	}
}

func TestScanRegistersHTMLFlags(t *testing.T) {
	cmd := newScanCommand(Services{})
	for _, name := range []string{"html", "port", "no-open"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s is not registered, so it will not appear in --help", name)
		}
	}
}

// --port is hand-parsed like every other scan flag, so a bad value has to
// fail the same deliberate way a bad index does rather than crash on Atoi's
// error or silently fall back to 0 (which would mean "pick a free port").
func TestScanRejectsANonIntegerPort(t *testing.T) {
	quietRender(t)
	cmd := newScanCommand(Services{})
	err := cmd.RunE(cmd, []string{"--port", "abc"})
	if err == nil {
		t.Fatal("a non-integer --port was accepted")
	}
	if !strings.Contains(err.Error(), "'--port'") || !strings.Contains(err.Error(), "'abc' is not a valid int") {
		t.Errorf("err = %v, want it to name the bad --port value", err)
	}
}

// --full streams Scout's own output to the terminal; serving a page instead is
// incoherent, so the combination is rejected rather than silently picking one.
func TestScanRejectsFullWithHTML(t *testing.T) {
	cmd := newScanCommand(Services{})
	err := cmd.RunE(cmd, []string{"1", "--full", "--html"})
	if err == nil {
		t.Fatal("--full with --html was accepted")
	}
	if !strings.Contains(err.Error(), "--full") || !strings.Contains(err.Error(), "--html") {
		t.Errorf("error %q names neither flag", err)
	}
}

// The kx flags must be consumed so they never reach the scanner.
func TestScanDoesNotForwardHTMLFlagsToTheScanner(t *testing.T) {
	rest := []string{"1", "--html", "--port", "9000", "--no-open", "--only-fixed"}
	html, rest := extractBool(rest, "--html")
	port, rest, err := extractString(rest, "--port", "")
	if err != nil {
		t.Fatalf("extractString returned %v", err)
	}
	noOpen, rest := extractBool(rest, "--no-open")

	if !html || !noOpen || port != "9000" {
		t.Fatalf("html=%v port=%q noOpen=%v", html, port, noOpen)
	}
	if len(rest) != 2 || rest[0] != "1" || rest[1] != "--only-fixed" {
		t.Errorf("rest = %v, want the index and the scanner's own flag", rest)
	}
}
