package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
	"github.com/jzills/kx/internal/state"
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

// captured is one image's scripted scanner result.
type captured struct {
	image          string
	stdout, stderr string
	code           int
}

// fakeScanner records invocations and replays scripted results.
//
// Results are keyed by image rather than handed out in call order: images are
// scanned concurrently, so call order is not the order they were asked for, and
// a positional fake would attribute one image's output to another. The mutex is
// for the same reason.
type fakeScanner struct {
	probeCode int
	probeErr  error
	captures  []captured

	mu    sync.Mutex
	calls int
}

func (f *fakeScanner) Scan([]string) (int, error) { return 0, nil }
func (f *fakeScanner) Probe([]string) (int, error) {
	return f.probeCode, f.probeErr
}
func (f *fakeScanner) Capture(argv []string) (string, string, int, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	image := argv[len(argv)-1]
	for _, result := range f.captures {
		if result.image == image {
			return result.stdout, result.stderr, result.code, nil
		}
	}
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

// A scanner that isn't installed at all reaches EnsureAvailable as an error
// from Probe rather than as a non-zero exit code, and used to be reported with
// exec's own generic wording — no engine name, and no install link. That only
// spared Docker Scout, whose preflight runs the `docker` binary that is
// present and answers "unknown command" for the missing plugin.
func TestEnsureAvailableReportsTheEngineWhenTheBinaryIsAbsent(t *testing.T) {
	for _, engine := range []struct{ name, link string }{
		{"trivy", "trivy.dev"},
		{"grype", "github.com/anchore/grype"},
	} {
		command := ScanCommand{
			Scanner: &fakeScanner{probeErr: scanner.NotFoundError{Binary: engine.name}},
			Status:  noStatus,
		}
		_, err := command.EnsureAvailable(engine.name)
		if err == nil {
			t.Fatalf("%s: EnsureAvailable succeeded with the binary absent", engine.name)
		}
		if !strings.Contains(err.Error(), engine.link) {
			t.Errorf("%s: error = %q, want it to link the install docs", engine.name, err)
		}
	}
}

// The absent-binary message must not read as a missing Kubernetes resource.
// IsNotFound requires a kubectl.Error before it matches anything, and
// scanner.NotFoundError isn't one — so "grype not found on PATH" never
// triggers the stale-state relist ("Run 'kx get <resource>' to refresh the
// list."), whatever words it happens to contain.
func TestEnsureAvailableAbsentBinaryIsNotMistakenForStaleState(t *testing.T) {
	command := ScanCommand{
		Scanner: &fakeScanner{probeErr: scanner.NotFoundError{Binary: "grype"}},
		Status:  noStatus,
	}
	_, err := command.EnsureAvailable("grype")
	if err == nil {
		t.Fatal("EnsureAvailable succeeded with the binary absent")
	}
	if IsNotFound(err) {
		t.Errorf("error = %q, which IsNotFound matches — it would trigger a stale-state refresh", err)
	}
}

// A preflight failure that is neither an exit code nor an absent binary — a
// binary present but not executable, say — is surfaced as it is. "Install it"
// would be the wrong advice for a file that is already there.
func TestEnsureAvailableSurfacesAnUnexpectedProbeFailure(t *testing.T) {
	command := ScanCommand{
		Scanner: &fakeScanner{probeErr: errors.New("permission denied")},
		Status:  noStatus,
	}
	_, err := command.EnsureAvailable("grype")
	if err == nil {
		t.Fatal("EnsureAvailable succeeded despite a probe failure")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want the underlying failure", err)
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
	scanner := &fakeScanner{captures: []captured{
		{image: "good:v1", stdout: `{"Results":[{"Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"}]}]}`},
		{image: "unpullable:v1", code: 1, stderr: "pulling...\nError: failed to pull image\n"},
		{image: "weird:v1", stdout: "not json"},
	}}

	rows, err := ScanCommand{Scanner: scanner, Status: noStatus}.
		Summarize("trivy", []string{"good:v1", "unpullable:v1", "weird:v1"}, nil)
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
	// 0 reads "no images found" rather than "0 images" — the same register kx
	// get, kx top and kx diag use for an empty result.
	if got := imagesNoun(0); got != "no images found" {
		t.Errorf("imagesNoun(0) = %q", got)
	}
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

// The selector and the banner label both describe the same scope and have to
// agree; they live on one type for that reason.
func TestScanScopeDescribesOneNamespace(t *testing.T) {
	scope := scanScope{Namespace: "prod"}
	if got := strings.Join(scope.selector(), " "); got != "-n prod" {
		t.Errorf("selector = %q, want %q", got, "-n prod")
	}
	if got := scope.label(); got != "prod" {
		t.Errorf("label = %q, want the namespace", got)
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

// A sweep that finds no workloads is not an error — kx diag treats an empty
// namespace the same way, and a pipeline running `kx scan -n staging` on a
// namespace nothing has been deployed to yet should not fail the build for it.
func TestCollectOnAnEmptyScopeSucceeds(t *testing.T) {
	kubectl := &fakeKubectl{output: `{"items":[]}`}
	command := ScanCommand{Kubectl: kubectl, Scanner: &fakeScanner{}, Status: noStatus}

	images, err := command.Collect(scanScope{All: true}, "scout")
	if err != nil {
		t.Fatalf("Collect on an empty scope: %v", err)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want none", images)
	}
}

// The end-to-end regression: `kx scan -n <empty>` used to exit 1 — the only
// one of kx get/top/diag/scan that treated "nothing there" as a failure.
// Driven through RunE, not just Collect, so the fix is pinned all the way to
// the exit code a pipeline actually sees.
func TestScanOnAnEmptyNamespaceSucceeds(t *testing.T) {
	sink := captureRender(t)
	services := Services{
		Kubectl: &fakeKubectl{namespace: "prod", output: `{"items":[]}`},
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config:  config.Default(),
		Scanner: &fakeScanner{},
	}
	cmd := newScanCommand(services)

	if err := cmd.RunE(cmd, []string{"--namespace", "empty"}); err != nil {
		t.Fatalf("kx scan -n empty: %v, want nil — an empty namespace is not a failure", err)
	}
	if !strings.Contains(sink.String(), "no images found") {
		t.Errorf("output = %q, want it to say nothing was found", sink.String())
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
	if page.Images[0].Counts["critical"] != 1 {
		t.Errorf("Counts = %+v, want the critical count carried through — it "+
			"feeds the page's severity bars", page.Images[0].Counts)
	}
	if page.Meta.Title != "t" {
		t.Errorf("Meta = %+v, want the meta passed in carried through unchanged", page.Meta)
	}
}

// A namespace sweep's page must carry the same "Mixed · " cross-kind label
// the terminal's ScopeBanner prints; an indexed scan's pageScope is built
// separately (in the index branch, string(kind)+"/"+name+" · "+namespace) and
// never calls this, which is what keeps it unlabelled.
func TestSweepPageScopeAddsTheMixedLabel(t *testing.T) {
	if got := sweepPageScope("prod"); got != "Mixed · prod" {
		t.Errorf("sweepPageScope(%q) = %q, want %q", "prod", got, "Mixed · prod")
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
	cmd := newScanCommand(argvServices(t))
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
	cmd := newScanCommand(argvServices(t))
	err := cmd.RunE(cmd, []string{"1", "--full", "--html"})
	if err == nil {
		t.Fatal("--full with --html was accepted")
	}
	if !strings.Contains(err.Error(), "--full") || !strings.Contains(err.Error(), "--html") {
		t.Errorf("error %q names neither flag", err)
	}
}

// --out implies --html, so --full is exactly as incoherent alongside it — and
// the error names --out, the flag actually typed, not --html.
func TestScanRejectsFullWithOut(t *testing.T) {
	cmd := newScanCommand(argvServices(t))
	err := cmd.RunE(cmd, []string{"1", "--full", "--out", filepath.Join(t.TempDir(), "r.html")})
	if err == nil {
		t.Fatal("--full with --out was accepted")
	}
	if !strings.Contains(err.Error(), "--full") || !strings.Contains(err.Error(), "--out") {
		t.Errorf("error %q, want it to name --full and --out (not --html, which was never typed)", err)
	}
}

// --no-open is extracted before --port, so a typo'd --no-open literal in
// RunE would leave the token "--no-open" in argv for the following
// extractString(rest, "--port", "") call to swallow as --port's own value,
// rather than --port failing on a missing one. Calling extractBool/
// extractString directly (as this file used to) would agree with whatever
// literal the test itself passes and could never see a typo in RunE; only
// real argv through cli.Execute proves the wiring (see argv_test.go's note
// on why argument handling is exercised this way).
func TestScanConsumesNoOpenBeforePort(t *testing.T) {
	quietRender(t)
	err := Execute(NewRoot(argvServices(t), "test"), []string{"scan", "--port", "--no-open"})
	if err == nil {
		t.Fatal("kx scan --port --no-open was accepted")
	}
	want := "flag needs an argument: --port"
	if err.Error() != want {
		t.Errorf("err = %q, want %q — a typo'd --no-open would let --port "+
			"swallow it as a value and report it as an invalid int instead",
			err.Error(), want)
	}
}

// Proves the --engine fallback reads the configured default rather than a
// hardcoded "scout" literal: an invalid Config.Engine must surface as an
// "unknown engine" error naming it, which could not happen if the fallback
// silently used "scout" (a name GetEngine always accepts) instead.
func TestScanOmittedEngineUsesConfiguredDefault(t *testing.T) {
	quietRender(t)
	services := argvServices(t)
	services.Config.Engine = "nonexistent-default"
	cmd := newScanCommand(services)
	err := cmd.RunE(cmd, []string{})
	if err == nil {
		t.Fatal("expected an error resolving the configured default engine")
	}
	if !strings.Contains(err.Error(), "nonexistent-default") {
		t.Errorf("err = %v, want it to name the configured default engine", err)
	}
}

// A scanner is free to colour its own stderr, and Grype does. Carried into a
// table cell verbatim, its reset sequence ends kx's styling mid-row and prints
// the escape's tail as text ("local-directory[0m").
func TestLastLineStripsScannerColour(t *testing.T) {
	stderr := "resolving image\n\x1b[31mfailed to fetch \x1b[1mregistry.invalid\x1b[0m\n"
	got := lastLine(stderr)
	want := "failed to fetch registry.invalid"
	if got != want {
		t.Errorf("lastLine = %q, want %q", got, want)
	}
}

// Stripping the escapes must not leave a cell that is empty or blank, which
// would read as a scan that failed for no reason at all.
func TestLastLineFallsBackWhenColourIsAllThereIs(t *testing.T) {
	if got := lastLine("\x1b[31m\x1b[0m\n"); got != "scan failed" {
		t.Errorf("lastLine = %q, want the generic fallback", got)
	}
}

// concurrentScanner records the order images arrive and how many scans are in
// flight at once, and can be told to finish them out of order.
type concurrentScanner struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	started  []string
	// release gates each image's completion, keyed by image, so a test can
	// finish them in whatever order it likes.
	release map[string]chan struct{}
	// hardErr is returned from Capture for this image, standing in for a
	// failure that is not the scanner's exit code — a missing binary.
	hardErr map[string]error
	// exitCode is the code returned for this image; anything non-zero records
	// an error on that image's row instead of aborting.
	exitCode map[string]int
	stdout   map[string]string
}

func newConcurrentScanner() *concurrentScanner {
	return &concurrentScanner{
		release:  map[string]chan struct{}{},
		hardErr:  map[string]error{},
		exitCode: map[string]int{},
		stdout:   map[string]string{},
	}
}

func (c *concurrentScanner) Scan([]string) (int, error)  { return 0, nil }
func (c *concurrentScanner) Probe([]string) (int, error) { return 0, nil }

func (c *concurrentScanner) Capture(argv []string) (string, string, int, error) {
	image := argv[len(argv)-1]

	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.peak {
		c.peak = c.inFlight
	}
	c.started = append(c.started, image)
	gate := c.release[image]
	c.mu.Unlock()

	if gate != nil {
		<-gate
	}

	c.mu.Lock()
	c.inFlight--
	out, code, err := c.stdout[image], c.exitCode[image], c.hardErr[image]
	c.mu.Unlock()

	return out, "scanner said no", code, err
}

func images(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("img-%02d", i))
	}
	return out
}

// Rows must come back in the order the images were resolved in, whatever order
// the scans finish — the table is indexed by position and the tests downstream
// pin it.
func TestSummarizeKeepsImageOrderWhenScansFinishOutOfOrder(t *testing.T) {
	list := images(4)
	fake := newConcurrentScanner()
	for _, image := range list {
		fake.release[image] = make(chan struct{})
		fake.stdout[image] = `{"Results":[]}`
	}
	// Finish in reverse.
	go func() {
		for i := len(list) - 1; i >= 0; i-- {
			close(fake.release[list[i]])
			time.Sleep(time.Millisecond)
		}
	}()

	command := ScanCommand{Scanner: fake, Status: noStatus}
	rows, err := command.Summarize("trivy", list, nil)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(rows) != len(list) {
		t.Fatalf("got %d rows, want %d", len(rows), len(list))
	}
	for i, row := range rows {
		if row.Image != list[i] {
			t.Errorf("rows[%d].Image = %q, want %q", i, row.Image, list[i])
		}
	}
}

// Scanners are heavy; running one per image at once would thrash a laptop
// scanning a real cluster's worth of images.
func TestSummarizeBoundsConcurrency(t *testing.T) {
	list := images(scanWorkers * 3)
	fake := newConcurrentScanner()
	// One gate for every image, so none can finish until the test says so.
	// Without it each scan completes before the next goroutine is scheduled and
	// the peak is 1 whether the semaphore works or not — the test would pass
	// against a serial implementation.
	gate := make(chan struct{})
	for _, image := range list {
		fake.stdout[image] = `{"Results":[]}`
		fake.release[image] = gate
	}

	go func() {
		// Wait for the semaphore to fill, then hold the gate a while longer.
		// The grace period is the whole point: releasing the moment it
		// saturates lets a *broken* (unbounded) implementation look bounded,
		// because the extra goroutines have not been scheduled yet. Holding
		// gives them time to pile up and be counted.
		deadline := time.After(5 * time.Second)
		for {
			fake.mu.Lock()
			saturated := fake.inFlight >= scanWorkers
			fake.mu.Unlock()
			if saturated {
				break
			}
			select {
			case <-deadline:
				close(gate)
				return
			case <-time.After(time.Millisecond):
			}
		}
		time.Sleep(200 * time.Millisecond)
		close(gate)
	}()

	command := ScanCommand{Scanner: fake, Status: noStatus}
	if _, err := command.Summarize("trivy", list, nil); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	fake.mu.Lock()
	peak := fake.peak
	fake.mu.Unlock()
	if peak > scanWorkers {
		t.Errorf("peak in-flight scans = %d, want at most %d", peak, scanWorkers)
	}
	if peak < scanWorkers {
		t.Errorf("peak in-flight scans = %d — the semaphore never saturated, so nothing ran in parallel", peak)
	}
}

// One unpullable image still records its failure on its own row rather than
// costing every other image its results — unchanged by running them at once.
func TestSummarizeStillContainsAPerImageFailure(t *testing.T) {
	list := images(3)
	fake := newConcurrentScanner()
	for _, image := range list {
		fake.stdout[image] = `{"Results":[]}`
	}
	fake.exitCode[list[1]] = 1

	command := ScanCommand{Scanner: fake, Status: noStatus}
	rows, err := command.Summarize("trivy", list, nil)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if rows[1].Error == "" {
		t.Errorf("rows[1] = %+v, want an error recorded on its own row", rows[1])
	}
	for _, i := range []int{0, 2} {
		if rows[i].Error != "" || rows[i].Counts == nil {
			t.Errorf("rows[%d] = %+v, want a clean result", i, rows[i])
		}
	}
}

// A failure that is not an exit code — the scanner binary vanishing mid-sweep —
// still aborts, and reports the earliest image's error, which is the one the
// serial version would have stopped on.
func TestSummarizeReportsTheEarliestHardFailure(t *testing.T) {
	list := images(4)
	fake := newConcurrentScanner()
	for _, image := range list {
		fake.stdout[image] = `{"Results":[]}`
	}
	fake.hardErr[list[1]] = errors.New("first failure")
	fake.hardErr[list[3]] = errors.New("later failure")

	command := ScanCommand{Scanner: fake, Status: noStatus}
	_, err := command.Summarize("trivy", list, nil)
	if err == nil {
		t.Fatal("Summarize succeeded despite a hard failure")
	}
	if !strings.Contains(err.Error(), "first failure") {
		t.Errorf("err = %q, want the earliest image's failure", err)
	}
}

// The progress callback is what turns a bare spinner into "scanning 7/30".
func TestSummarizeReportsProgressOncePerImage(t *testing.T) {
	list := images(6)
	fake := newConcurrentScanner()
	for _, image := range list {
		fake.stdout[image] = `{"Results":[]}`
	}

	var done atomic.Int64
	command := ScanCommand{Scanner: fake, Status: noStatus}
	if _, err := command.Summarize("trivy", list, func() { done.Add(1) }); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got := done.Load(); got != int64(len(list)) {
		t.Errorf("progress fired %d times, want %d", got, len(list))
	}
}

// The document's subject comes from the scan's own scope, not from the caption
// printed above the table. "Mixed · " is a cross-kind display label the summary
// prints above itself, and handing that to the document is the regression this
// guards — through the real RunE, since the wiring is where it would happen.
func TestScanJSONSweepNamesTheBareNamespace(t *testing.T) {
	if !strings.Contains(sweepPageScope("prod"), "Mixed") {
		t.Fatal("sweepPageScope no longer carries the label this guards against")
	}
	sink := captureRender(t)
	services, _ := scanHTMLServices(t, `{"Results":[]}`)
	cmd := newScanCommand(services)
	cmd.SetContext(stoppedContext())

	if err := cmd.RunE(cmd, []string{
		"--engine", "trivy", "--namespace", "prod", "--json",
	}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	document := decodeJSON(t, sink.String())
	if got := document["namespace"]; got != "prod" {
		t.Errorf("namespace = %v, want the bare namespace", got)
	}
	if strings.Contains(sink.String(), "Mixed") {
		t.Errorf("the caption's display label reached the document:\n%s", sink.String())
	}
}

// -A reaches the document as a boolean, not as the literal words "all
// namespaces" — which is what the caption says, and what no consumer can tell
// from a namespace actually called that.
func TestScanJSONClusterWideSweepIsABoolean(t *testing.T) {
	sink := captureRender(t)
	services, _ := scanHTMLServices(t, `{"Results":[]}`)
	cmd := newScanCommand(services)
	cmd.SetContext(stoppedContext())

	if err := cmd.RunE(cmd, []string{"--engine", "trivy", "-A", "--json"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	document := decodeJSON(t, sink.String())
	if got := document["allNamespaces"]; got != true {
		t.Errorf("allNamespaces = %v, want true", got)
	}
	if strings.Contains(sink.String(), render.AllNamespaces) {
		t.Errorf("the caption's wording reached the document:\n%s", sink.String())
	}
}

// The bound was always scanWorkers, but the shape that enforced it launched a
// goroutine per image and had all but scanWorkers of them sit blocked on a
// semaphore for the whole sweep. A namespace sweep resolves hundreds of unique
// images, so that was hundreds of goroutines and their stacks buying nothing.
//
// Counted while every scan is held, when the pool is at its widest: a fixed
// pool parks the caller on the position channel and holds scanWorkers workers,
// where the old shape held one goroutine for every image not yet started.
func TestSummarizeDoesNotLaunchAGoroutinePerImage(t *testing.T) {
	const count = 60
	list := images(count)
	fake := newConcurrentScanner()
	gate := make(chan struct{})
	for _, image := range list {
		fake.stdout[image] = `{"Results":[]}`
		fake.release[image] = gate
	}

	before := runtime.NumGoroutine()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := (ScanCommand{Scanner: fake, Status: noStatus}).
			Summarize("trivy", list, nil); err != nil {
			t.Errorf("Summarize: %v", err)
		}
	}()

	// Wait until the pool is saturated — every worker is inside a held scan,
	// which is the moment the old shape had the rest of its goroutines parked.
	for {
		fake.mu.Lock()
		started := len(fake.started)
		fake.mu.Unlock()
		if started >= scanWorkers {
			break
		}
		runtime.Gosched()
	}
	peak := runtime.NumGoroutine() - before

	close(gate)
	<-done

	// Generous: the pool costs scanWorkers plus the goroutine driving
	// Summarize. Anything near `count` is a goroutine per image.
	if limit := scanWorkers + 8; peak > limit {
		t.Errorf("Summarize held %d extra goroutines for %d images, want at most %d",
			peak, count, limit)
	}
}

// --fail-on needs findings kx has parsed, and --full hands the scanner the
// terminal and never parses a thing — the same reason --json and --full are
// refused together. Accepting the pair and then exiting 0 whatever was found
// is the one outcome a gate must not have.
func TestScanRejectsFullWithFailOn(t *testing.T) {
	cmd := newScanCommand(argvServices(t))
	err := cmd.RunE(cmd, []string{"1", "--full", "--fail-on", "critical"})
	if err == nil {
		t.Fatal("--full with --fail-on was accepted")
	}
	if !strings.Contains(err.Error(), "--full") || !strings.Contains(err.Error(), "--fail-on") {
		t.Errorf("error %q names neither flag", err)
	}
}

// trivyCritical is one CRITICAL vulnerability in trivy's own JSON shape.
const trivyCritical = `{"Results":[{"Vulnerabilities":[` +
	`{"VulnerabilityID":"CVE-2024-0001","Severity":"CRITICAL",` +
	`"PkgName":"openssl","InstalledVersion":"1.0"}]}]}`

// scanHTMLServices drives newScanCommand's RunE all the way through a sweep,
// a scan and the html branch, which needs a scanner Services can substitute —
// the command used to build an ExecService inline and shell out for real.
func scanHTMLServices(t *testing.T, stdout string) (Services, *fakeScanner) {
	t.Helper()
	fake := &fakeScanner{captures: []captured{{image: "api:v1", stdout: stdout}}}
	return Services{
		Kubectl: &fakeKubectl{
			namespace: "prod",
			output:    `{"items":[{"kind":"Pod","spec":{"containers":[{"image":"api:v1"}]}}]}`,
		},
		State:   &state.Service{MaxHistory: 10, Path: filepath.Join(t.TempDir(), "state.json")},
		Config:  config.Default(),
		Scanner: fake,
	}, fake
}

// The twin of the diag gate: --html said where the findings go, and returning
// servePage's nil instead of scanGate's exit code meant a pipeline that added
// --html to publish its report stopped failing on what the report contained.
func TestScanWithHTMLStillAppliesTheFailOnGate(t *testing.T) {
	quietRender(t)
	services, _ := scanHTMLServices(t, trivyCritical)
	cmd := newScanCommand(services)
	cmd.SetContext(stoppedContext())

	var silent SilentError
	err := cmd.RunE(cmd, []string{
		"--engine", "trivy", "--namespace", "prod",
		"--html", "--no-open", "--fail-on", "critical",
	})
	if !errors.As(err, &silent) {
		t.Fatalf("RunE = %v, want --fail-on to exit %d through the html branch", err, findingsExitCode)
	}
	if silent.Code != findingsExitCode {
		t.Errorf("exit code = %d, want %d", silent.Code, findingsExitCode)
	}
}

// The other half: a clean scan still exits 0 with --html set, so the test above
// is not passing against a gate that always fires.
func TestScanWithHTMLPassesACleanGate(t *testing.T) {
	quietRender(t)
	services, _ := scanHTMLServices(t, `{"Results":[]}`)
	cmd := newScanCommand(services)
	cmd.SetContext(stoppedContext())

	err := cmd.RunE(cmd, []string{
		"--engine", "trivy", "--namespace", "prod",
		"--html", "--no-open", "--fail-on", "critical",
	})
	if err != nil {
		t.Errorf("RunE = %v, want nil for a scan with nothing at the threshold", err)
	}
}
