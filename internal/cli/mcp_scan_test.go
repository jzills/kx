package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/scanner"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// trivyReport is Trivy's --format json output carrying these findings, each
// given as {id, severity}.
func trivyReport(t *testing.T, findings ...[2]string) string {
	t.Helper()
	type vulnerability struct {
		VulnerabilityID string
		PkgName         string
		Severity        string
	}
	vulnerabilities := make([]vulnerability, 0, len(findings))
	for _, finding := range findings {
		vulnerabilities = append(vulnerabilities, vulnerability{finding[0], "pkg", finding[1]})
	}
	encoded, err := json.Marshal(map[string]any{
		"Results": []any{map[string]any{"Vulnerabilities": vulnerabilities}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// workloadJSON is a Deployment whose pod template runs these images.
func workloadJSON(images ...string) string {
	containers := make([]string, 0, len(images))
	for _, image := range images {
		containers = append(containers, `{"image":"`+image+`"}`)
	}
	return `{"kind":"Deployment","spec":{"template":{"spec":{"containers":[` +
		strings.Join(containers, ",") + `]}}}}`
}

// scanDeps is a server whose kubectl answers with outputs in turn and whose
// scanner is fake, configured for Trivy so the canned reports parse.
func scanDeps(t *testing.T, fake *fakeScanner, outputs ...string) (mcpDeps, *recordingKubectl) {
	t.Helper()
	kube := &recordingKubectl{namespace: "prod", outputs: outputs}
	deps := mcpTestDeps(t, kube)
	deps.Config.Engine = "trivy"
	deps.Scanner = fake
	return deps, kube
}

var deployTarget = map[string]any{"kind": "deploy", "name": "api", "namespace": "prod"}

func findingIDs(image jsonImage) []string {
	ids := make([]string, 0, len(image.Findings))
	for _, finding := range image.Findings {
		ids = append(ids, finding.ID)
	}
	return ids
}

// Counts cover every severity, but only findings at minSeverity or worse are
// listed — worst first, in the scanner's order within a severity.
func TestMCPScanListsFindingsFromMinSeverityWorstFirst(t *testing.T) {
	fake := &fakeScanner{captures: []captured{{image: "api:v1", stdout: trivyReport(t,
		[2]string{"L1", "LOW"}, [2]string{"C1", "CRITICAL"}, [2]string{"M1", "MEDIUM"},
		[2]string{"H1", "HIGH"}, [2]string{"C2", "CRITICAL"}, [2]string{"U1", "UNKNOWN"},
	)}}}

	for _, tc := range []struct {
		minSeverity string
		want        []string
	}{
		{"", []string{"C1", "C2", "H1"}}, // the default is high
		{"critical", []string{"C1", "C2"}},
		{"medium", []string{"C1", "C2", "H1", "M1"}},
		{"LOW", []string{"C1", "C2", "H1", "M1", "L1"}},
	} {
		deps, kube := scanDeps(t, fake, workloadJSON("api:v1"))
		session := connectMCP(t, deps)
		args := map[string]any{"target": deployTarget}
		if tc.minSeverity != "" {
			args["minSeverity"] = tc.minSeverity
		}
		var out scanOutput
		decodeStructured(t, callTool(t, session, "scan", args), &out)

		if out.Context != "test" {
			t.Errorf("context = %q, want test", out.Context)
		}
		if out.Scan.Kind != kinds.Deployment || out.Scan.Name != "api" || out.Scan.Namespace != "prod" {
			t.Errorf("subject = %s/%s in %s, want Deployment/api in prod",
				out.Scan.Kind, out.Scan.Name, out.Scan.Namespace)
		}
		want := []string{"get", "Deployment", "api", "-n", "prod", "-o", "json"}
		if len(kube.runs) != 1 || !slices.Equal(kube.runs[0], want) {
			t.Errorf("kubectl runs = %v, want [%v]", kube.runs, want)
		}
		if len(out.Scan.Images) != 1 {
			t.Fatalf("%d images, want 1", len(out.Scan.Images))
		}
		image := out.Scan.Images[0]
		if got := findingIDs(image); !slices.Equal(got, tc.want) {
			t.Errorf("minSeverity %q: findings %v, want %v", tc.minSeverity, got, tc.want)
		}
		wantCounts := map[string]int{"critical": 2, "high": 1, "medium": 1, "low": 1, "unspecified": 1}
		for severity, count := range wantCounts {
			if image.Counts[severity] != count {
				t.Errorf("minSeverity %q: counts %v, want %v", tc.minSeverity, image.Counts, wantCounts)
				break
			}
		}
		if image.Truncated != 0 {
			t.Errorf("minSeverity %q: truncated %d, want 0 — nothing was cut by the limit",
				tc.minSeverity, image.Truncated)
		}
	}
}

// The engine defaults to the configured one; an explicit engine replaces it.
func TestMCPScanUsesTheConfiguredEngineUnlessOneIsGiven(t *testing.T) {
	for _, tc := range []struct {
		configured, given string
		want              scanner.Engine
	}{
		{"trivy", "", scanner.Trivy{}},
		{"scout", "trivy", scanner.Trivy{}},
		{"trivy", "grype", scanner.Grype{}},
	} {
		fake := &fakeScanner{captures: []captured{{image: "api:v1", stdout: "{}"}}}
		deps, _ := scanDeps(t, fake, workloadJSON("api:v1"))
		deps.Config.Engine = tc.configured
		session := connectMCP(t, deps)
		args := map[string]any{"target": deployTarget}
		if tc.given != "" {
			args["engine"] = tc.given
		}
		callTool(t, session, "scan", args)
		want := [][]string{tc.want.PreflightArgv(), tc.want.SummaryArgv("api:v1")}
		if len(fake.argv) != 2 || !slices.Equal(fake.argv[0], want[0]) || !slices.Equal(fake.argv[1], want[1]) {
			t.Errorf("configured %q, given %q: scanner argv %v, want %v", tc.configured, tc.given, fake.argv, want)
		}
	}
}

// limit caps the findings listed per image, and each image says how many of
// the findings it would have listed were cut.
func TestMCPScanLimitsFindingsPerImage(t *testing.T) {
	many := make([][2]string, 0, 25)
	for i := range 25 {
		many = append(many, [2]string{"H" + string(rune('A'+i)), "HIGH"})
	}
	fake := &fakeScanner{captures: []captured{
		{image: "big:v1", stdout: trivyReport(t, many...)},
		{image: "small:v1", stdout: trivyReport(t, [2]string{"C1", "CRITICAL"}, [2]string{"L1", "LOW"})},
	}}

	for _, tc := range []struct {
		limit         int
		wantBig       int
		wantTruncated int
	}{
		{0, 20, 5}, // the default is 20
		{3, 3, 22},
		{500, 25, 0}, // capped at 200, which is more than there are
	} {
		deps, _ := scanDeps(t, fake, workloadJSON("big:v1", "small:v1"))
		session := connectMCP(t, deps)
		args := map[string]any{"target": deployTarget}
		if tc.limit != 0 {
			args["limit"] = tc.limit
		}
		var out scanOutput
		decodeStructured(t, callTool(t, session, "scan", args), &out)
		if len(out.Scan.Images) != 2 {
			t.Fatalf("%d images, want 2", len(out.Scan.Images))
		}
		big, small := out.Scan.Images[0], out.Scan.Images[1]
		if len(big.Findings) != tc.wantBig || big.Truncated != tc.wantTruncated {
			t.Errorf("limit %d: big lists %d, truncated %d; want %d, truncated %d",
				tc.limit, len(big.Findings), big.Truncated, tc.wantBig, tc.wantTruncated)
		}
		if big.Counts["high"] != 25 {
			t.Errorf("limit %d: big counts %v, want all 25 highs counted", tc.limit, big.Counts)
		}
		// The low finding sits below minSeverity, so it was never going to be
		// listed: it is not something the limit cut.
		if len(small.Findings) != 1 || small.Truncated != 0 {
			t.Errorf("limit %d: small lists %v, truncated %d; want [C1], truncated 0",
				tc.limit, findingIDs(small), small.Truncated)
		}
	}
}

// With no target, scan sweeps a namespace through Collect's own selector.
func TestMCPScanSweepsANamespace(t *testing.T) {
	list := `{"items":[` + workloadJSON("api:v1", "shared:v1") + `,` + workloadJSON("shared:v1", "web:v1") + `]}`
	for _, tc := range []struct {
		args        map[string]any
		wantArgs    []string
		wantSubject scanSubject
	}{
		{map[string]any{}, []string{"get", namespaceScanKinds, "-n", "prod", "-o", "json"},
			scanSubject{Namespace: "prod"}},
		{map[string]any{"namespace": "staging"}, []string{"get", namespaceScanKinds, "-n", "staging", "-o", "json"},
			scanSubject{Namespace: "staging"}},
		{map[string]any{"allNamespaces": true}, []string{"get", namespaceScanKinds, "--all-namespaces", "-o", "json"},
			scanSubject{AllNamespaces: true}},
	} {
		fake := &fakeScanner{captures: []captured{
			{image: "api:v1", stdout: "{}"}, {image: "shared:v1", stdout: "{}"}, {image: "web:v1", stdout: "{}"},
		}}
		deps, kube := scanDeps(t, fake, list)
		session := connectMCP(t, deps)
		var out scanOutput
		decodeStructured(t, callTool(t, session, "scan", tc.args), &out)

		if len(kube.runs) != 1 || !slices.Equal(kube.runs[0], tc.wantArgs) {
			t.Errorf("%v: kubectl runs = %v, want [%v]", tc.args, kube.runs, tc.wantArgs)
		}
		got := scanSubject{Kind: out.Scan.Kind, Name: out.Scan.Name,
			Namespace: out.Scan.Namespace, AllNamespaces: out.Scan.AllNamespaces}
		if got != tc.wantSubject {
			t.Errorf("%v: subject %+v, want %+v", tc.args, got, tc.wantSubject)
		}
		var images []string
		for _, image := range out.Scan.Images {
			images = append(images, image.Image)
		}
		if want := []string{"api:v1", "shared:v1", "web:v1"}; !slices.Equal(images, want) {
			t.Errorf("%v: images %v, want %v", tc.args, images, want)
		}
	}
}

// Every refusal comes back as the tool's error before any image is scanned.
func TestMCPScanRefusals(t *testing.T) {
	_, unknownEngine := scanner.GetEngine("clair")
	for _, tc := range []struct {
		name string
		args map[string]any
		fake *fakeScanner
		want string
	}{
		{"unsupported kind", map[string]any{"target": map[string]any{"kind": "svc", "name": "api"}},
			&fakeScanner{}, unsupportedKindError("scan", kinds.Service, scannableKinds).Error()},
		{"unknown engine", map[string]any{"target": deployTarget, "engine": "clair"},
			&fakeScanner{}, unknownEngine.Error()},
		{"flag-shaped engine", map[string]any{"target": deployTarget, "engine": "--output=/tmp/x"},
			&fakeScanner{}, "Unknown engine '--output=/tmp/x'."},
		{"unavailable engine", map[string]any{"target": deployTarget},
			&fakeScanner{probeCode: 1}, scanner.Trivy{}.UnavailableMessage()},
		{"unknown severity", map[string]any{"target": deployTarget, "minSeverity": "unspecified"},
			&fakeScanner{}, "Invalid value for 'minSeverity': 'unspecified'. Accepted values: critical, high, medium, low."},
		{"target beside a namespace", map[string]any{"target": deployTarget, "namespace": "prod"},
			&fakeScanner{}, "'namespace' and 'allNamespaces' apply without a target"},
		{"both scopes", map[string]any{"namespace": "prod", "allNamespaces": true},
			&fakeScanner{}, "'allNamespaces' and 'namespace' cannot be combined."},
		{"flag-shaped namespace", map[string]any{"namespace": "-A"},
			&fakeScanner{}, "namespace '-A'"},
	} {
		deps, kube := scanDeps(t, tc.fake, workloadJSON("api:v1"))
		session := connectMCP(t, deps)
		result := callTool(t, session, "scan", tc.args)
		if !result.IsError || !strings.Contains(toolText(result), tc.want) {
			t.Errorf("%s: got %q (error %v), want an error containing %q",
				tc.name, toolText(result), result.IsError, tc.want)
		}
		if tc.fake.calls != 0 {
			t.Errorf("%s: scanned %d images, want none", tc.name, tc.fake.calls)
		}
		if tc.name == "unknown engine" && len(kube.runs) != 0 {
			t.Errorf("%s: kubectl ran %v before the engine was checked", tc.name, kube.runs)
		}
	}
}

// One image the scanner fails on keeps its error row, and the others still
// come back.
func TestMCPScanKeepsAFailedImagesRow(t *testing.T) {
	fake := &fakeScanner{captures: []captured{
		{image: "gone:v1", code: 1, stderr: "Error: failed to pull image\n"},
		{image: "api:v1", stdout: trivyReport(t, [2]string{"C1", "CRITICAL"})},
	}}
	deps, _ := scanDeps(t, fake, workloadJSON("gone:v1", "api:v1"))
	session := connectMCP(t, deps)
	var out scanOutput
	decodeStructured(t, callTool(t, session, "scan", map[string]any{"target": deployTarget}), &out)
	if len(out.Scan.Images) != 2 {
		t.Fatalf("%d images, want 2", len(out.Scan.Images))
	}
	gone, api := out.Scan.Images[0], out.Scan.Images[1]
	if gone.Error != "Error: failed to pull image" || gone.Counts != nil {
		t.Errorf("failed image = %+v, want its error and no counts", gone)
	}
	if api.Error != "" || !slices.Equal(findingIDs(api), []string{"C1"}) {
		t.Errorf("scanned image = %+v, want C1 listed", api)
	}
}

// A namespace sweep reads images other people wrote. One shaped like a flag
// comes back as its own error row and never reaches the scanner's argv.
func TestMCPScanRefusesAFlagShapedImageInASweep(t *testing.T) {
	list := `{"items":[` + workloadJSON("api:v1", "--output=/home/u/.bashrc") + `]}`
	fake := &fakeScanner{captures: []captured{{image: "api:v1", stdout: trivyReport(t, [2]string{"C1", "CRITICAL"})}}}
	deps, _ := scanDeps(t, fake, list)
	session := connectMCP(t, deps)
	var out scanOutput
	decodeStructured(t, callTool(t, session, "scan", map[string]any{"allNamespaces": true}), &out)
	if len(out.Scan.Images) != 2 {
		t.Fatalf("%d images, want 2", len(out.Scan.Images))
	}
	api, hostile := out.Scan.Images[0], out.Scan.Images[1]
	if !slices.Equal(findingIDs(api), []string{"C1"}) {
		t.Errorf("api:v1 = %+v, want it scanned", api)
	}
	if !strings.Contains(hostile.Error, "is not an image reference") {
		t.Errorf("flag-shaped image = %+v, want an error row refusing it", hostile)
	}
	for _, argv := range fake.argv {
		if slices.Contains(argv, "--output=/home/u/.bashrc") {
			t.Errorf("scanner called with %v", argv)
		}
	}
}

// A client that sends a progress token hears once per image scanned; one
// that doesn't hears nothing.
func TestMCPScanReportsProgressWhenAsked(t *testing.T) {
	images := []string{"a:v1", "b:v1", "c:v1"}
	var captures []captured
	for _, image := range images {
		captures = append(captures, captured{image: image, stdout: "{}"})
	}
	fake := &fakeScanner{captures: captures}
	deps, _ := scanDeps(t, fake, workloadJSON(images...), workloadJSON(images...))

	notes := make(chan *mcp.ProgressNotificationParams, 16)
	session := connectMCPWith(t, deps, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			notes <- req.Params
		},
	})

	// First without a token, then with one: notifications on one connection
	// arrive in order, so any the first call wrongly sent land before the
	// second call's and are caught by their token.
	callTool(t, session, "scan", map[string]any{"target": deployTarget})
	params := &mcp.CallToolParams{Name: "scan", Arguments: map[string]any{"target": deployTarget}}
	params.SetProgressToken("sweep-1")
	result, err := session.CallTool(context.Background(), params)
	if err != nil || result.IsError {
		t.Fatalf("scan with a progress token: %v %s", err, toolText(result))
	}

	var progress []float64
	for len(progress) < len(images) {
		select {
		case note := <-notes:
			if note.ProgressToken != "sweep-1" {
				t.Fatalf("notification for token %v — the call without a token must send none", note.ProgressToken)
			}
			if note.Total != float64(len(images)) {
				t.Errorf("total = %v, want %d", note.Total, len(images))
			}
			if !strings.HasPrefix(note.Message, "scanned ") || !strings.HasSuffix(note.Message, " of 3 images") {
				t.Errorf("message = %q, want \"scanned N of 3 images\"", note.Message)
			}
			progress = append(progress, note.Progress)
		case <-time.After(5 * time.Second):
			t.Fatalf("heard %d progress notifications, want %d", len(progress), len(images))
		}
	}
	// In arrival order: MCP requires progress to rise with every
	// notification, and two workers finishing together must not send 2
	// before 1.
	if !slices.Equal(progress, []float64{1, 2, 3}) {
		t.Errorf("progress = %v in arrival order, want 1, 2, 3", progress)
	}
	select {
	case note := <-notes:
		t.Errorf("an extra notification: %+v", note)
	case <-time.After(100 * time.Millisecond):
	}
}

// Progress counts images finished, not positions: the second image finishes
// first here (the first is held until its notification reaches the client),
// and must still be reported as 1. The race between two workers' count and
// send is TestMCPScanReportsProgressWhenAsked's to catch — no fake can pause
// a worker between the two, so that one is caught by repetition.
func TestMCPScanProgressRisesWhenImagesFinishOutOfOrder(t *testing.T) {
	notes := make(chan float64, 8)
	firstGo := make(chan struct{})
	fake := &fakeScanner{
		captures: []captured{{image: "a:v1", stdout: "{}"}, {image: "b:v1", stdout: "{}"}},
		capturing: func(argv []string) {
			if argv[len(argv)-1] == "a:v1" {
				<-firstGo
			}
		},
	}
	deps, _ := scanDeps(t, fake, workloadJSON("a:v1", "b:v1"))
	session := connectMCPWith(t, deps, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			notes <- req.Params.Progress
		},
	})
	params := &mcp.CallToolParams{Name: "scan", Arguments: map[string]any{"target": deployTarget}}
	params.SetProgressToken("order")
	finished := make(chan error, 1)
	go func() {
		_, err := session.CallTool(context.Background(), params)
		finished <- err
	}()

	var progress []float64
	select {
	case p := <-notes:
		progress = append(progress, p)
	case <-time.After(5 * time.Second):
		close(firstGo)
		t.Fatal("no progress for the image that finished first")
	}
	close(firstGo)
	select {
	case p := <-notes:
		progress = append(progress, p)
	case <-time.After(5 * time.Second):
		t.Fatal("no progress for the second image")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(progress, []float64{1, 2}) {
		t.Errorf("progress = %v in arrival order, want 1, 2", progress)
	}
}

// One scan at a time: the scanner pool is sized for memory, so two agents'
// sweeps must not each bring their own. A second scan resolves, then waits
// for the first to finish before any of its images starts.
func TestMCPScanRunsOneScanAtATime(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	fake := &fakeScanner{
		captures: []captured{{image: "a:v1", stdout: "{}"}, {image: "b:v1", stdout: "{}"}},
		capturing: func(argv []string) {
			entered <- argv[len(argv)-1]
			<-release
		},
	}
	deps, _ := scanDeps(t, fake, workloadJSON("a:v1"), workloadJSON("b:v1"))
	session := connectMCP(t, deps)
	// Both deferred ahead of the sessions' cleanups: a scan stuck waiting
	// for the slot is cancelled rather than hanging the session's close.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer unblock()

	scan := func(done chan<- error) {
		_, err := session.CallTool(ctx,
			&mcp.CallToolParams{Name: "scan", Arguments: map[string]any{"target": deployTarget}})
		done <- err
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go scan(first)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first scan never reached the scanner")
	}
	go scan(second)
	select {
	case image := <-entered:
		t.Fatalf("%s started while another scan was running", image)
	case <-time.After(300 * time.Millisecond):
	}

	unblock()
	for _, done := range []chan error{first, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a scan did not finish after the scanner was released")
		}
	}
	if image := <-entered; image != "b:v1" {
		t.Errorf("second scan scanned %s, want b:v1", image)
	}
}

// A caller that gives up stops the scan: images not yet started never are,
// and a scan still queued for the slot never starts. The server's handler
// has to have returned for the next scan to get the slot, which is what the
// last call proves.
func TestMCPScanStopsWhenTheCallerCancels(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	fake := &fakeScanner{capturing: func(argv []string) {
		entered <- argv[len(argv)-1]
		<-release
	}}
	deps, _ := scanDeps(t, fake,
		workloadJSON("a:v1", "b:v1", "c:v1", "d:v1"), // the sweep that is cancelled
		workloadJSON("queued:v1"),                    // the scan cancelled while queued
		workloadJSON("after:v1"),                     // the scan that proves the slot is free
	)
	session := connectMCP(t, deps)
	testCtx, cancelTest := context.WithCancel(context.Background())
	defer cancelTest()
	defer unblock()

	call := func(ctx context.Context, done chan<- error) {
		_, err := session.CallTool(ctx,
			&mcp.CallToolParams{Name: "scan", Arguments: map[string]any{"target": deployTarget}})
		done <- err
	}
	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	defer cancelSweep()
	sweep := make(chan error, 1)
	go call(sweepCtx, sweep)
	for range scanWorkers {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the sweep's workers never reached the scanner")
		}
	}
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go call(queuedCtx, queued)
	time.Sleep(200 * time.Millisecond) // let it resolve and queue for the slot

	cancelQueued()
	cancelSweep()
	for _, done := range []chan error{sweep, queued} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a cancelled call did not return")
		}
	}
	// The client returns at once and sends notifications/cancelled from a
	// goroutine afterwards, so the server's request context is cancelled a
	// moment after the calls above return. Nothing observable marks that
	// moment, so give it one before freeing the workers.
	time.Sleep(300 * time.Millisecond)
	unblock()

	after := make(chan error, 1)
	go call(testCtx, after)
	select {
	case err := <-after:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next scan never got the slot — the cancelled scan still holds it")
	}
	close(entered)
	var started []string
	for image := range entered {
		started = append(started, image)
	}
	if !slices.Equal(started, []string{"after:v1"}) {
		t.Errorf("after cancelling, scanned %v; want only after:v1", started)
	}
}

// A caller that gives up while its scan is queued behind another leaves the
// queue at once, rather than holding a goroutine until the slot frees and
// only then noticing.
func TestMCPScanLeavesTheQueueWhenCancelled(t *testing.T) {
	fake := &fakeScanner{captures: []captured{{image: "a:v1", stdout: "{}"}}}
	deps, _ := scanDeps(t, fake, workloadJSON("a:v1"))
	deps.scanSlots <- struct{}{} // another scan holds the slot
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := deps.scan(ctx, &mcp.CallToolRequest{}, scanInput{Target: &mcpTarget{Kind: "deploy", Name: "api"}})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled scan stayed queued for the slot")
	}
	if fake.calls != 0 {
		t.Errorf("scanned %d images, want none", fake.calls)
	}
}

// A scan can take minutes, so the server lock is held only while resolving
// images: while the scanner runs, another tool call completes.
func TestMCPScanReleasesTheLockWhileScanning(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	fake := &fakeScanner{
		captures: []captured{{image: "api:v1", stdout: "{}"}},
		capturing: func([]string) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		},
	}
	deps, _ := scanDeps(t, fake, workloadJSON("api:v1"))
	session := connectMCP(t, deps)
	// Deferred ahead of the sessions' cleanups, so a failure below still lets
	// the scan finish rather than hanging the test on close.
	defer unblock()

	scanned := make(chan error, 1)
	go func() {
		_, err := session.CallTool(context.Background(),
			&mcp.CallToolParams{Name: "scan", Arguments: map[string]any{"target": deployTarget}})
		scanned <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the scan never reached the scanner")
	}

	listed := make(chan error, 1)
	go func() {
		_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_marks", Arguments: map[string]any{}})
		listed <- err
	}()
	select {
	case err := <-listed:
		if err != nil {
			t.Fatalf("list_marks: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("list_marks did not complete while a scan was running — the scan is holding the server lock")
	}

	unblock()
	select {
	case err := <-scanned:
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the scan did not finish after the scanner was released")
	}
}
