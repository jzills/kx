package cli

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
)

// interleavedKubectl is a fakeKubectl whose watch runs between after the
// line at position at, the way another terminal's command lands between two
// of a watch's events.
type interleavedKubectl struct {
	*fakeKubectl
	at      int
	between func()
}

func (k interleavedKubectl) Watch(args []string, onLine func(string) error) error {
	for i, line := range k.watchLines {
		if err := onLine(line); err != nil {
			return err
		}
		if i == k.at {
			k.between()
		}
	}
	return nil
}

// framesOf splits a terminal's output into the frames the redraw drew.
func framesOf(output string) []string {
	var frames []string
	for _, frame := range strings.Split(output, "\x1b[J") {
		if strings.Contains(frame, "NAME") {
			frames = append(frames, frame)
		}
	}
	return frames
}

// watchTerminal runs argv on a terminal and returns its frames and services.
func watchTerminal(t *testing.T, kube kubectl.Service, argv ...string) ([]string, Services) {
	t.Helper()
	services := switchServices(t, kube)
	var term terminalBuffer
	render.SetOutput(&term, &bytes.Buffer{}, "github-dark")
	if err := executeArgv(newGetCommand(services), argv); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	return framesOf(term.String()), services
}

var numberedRow = regexp.MustCompile(`(?m)^\s*(\d+)\s+(\S+)\s`)

// numbersIn maps each row's NAME to the index drawn beside it.
func numbersIn(frame string) map[string]string {
	numbers := map[string]string{}
	for _, match := range numberedRow.FindAllStringSubmatch(frame, -1) {
		numbers[match[2]] = match[1]
	}
	return numbers
}

// churn is a watch in which db is deleted, cache appears, and db comes back.
var churn = []string{
	watchHeader,
	"ADDED      api    1/1     Running            2          1d",
	"ADDED      db     0/1     CrashLoopBackOff   14         1d",
	"ADDED      web    1/1     Running            0          1d",
	"DELETED    db     0/1     CrashLoopBackOff   14         1d",
	"ADDED      cache  1/1     Running            0          1s",
	"ADDED      db     0/1     Pending            0          1s",
}

// A row keeps the index it first appeared with: a deleted row's index is
// never handed to the row after it, and a name that comes back gets its old
// one. The frame after the delete is the control for the reuse: db must be
// gone from it, or the last frame's 2 could be the original row.
func TestWatchNumbersEachRowByItsFirstAppearance(t *testing.T) {
	frames, _ := watchTerminal(t, &fakeKubectl{namespace: "prod", watchLines: churn}, "pods", "-w")
	if len(frames) != 6 {
		t.Fatalf("drew %d frames, want one per row event", len(frames))
	}
	if _, ok := numbersIn(frames[3])["db"]; ok {
		t.Fatalf("frame after the delete still shows db:\n%s", frames[3])
	}
	want := map[string]string{"api": "1", "db": "2", "web": "3", "cache": "4"}
	if got := numbersIn(frames[5]); !maps.Equal(got, want) {
		t.Errorf("last frame numbers %v, want %v:\n%s", got, want, frames[5])
	}
	if !strings.Contains(frames[5], numberedWatchNote) {
		t.Errorf("last frame has no %q note:\n%s", numberedWatchNote, frames[5])
	}
}

// Sorted, the rows move and their indexes go with them. The unsorted order
// is api,db,web, so a sort that renumbered by position would give db 1.
func TestWatchSortMovesRowsWithTheirIndexes(t *testing.T) {
	frames, _ := watchTerminal(t, &fakeKubectl{namespace: "prod", watchLines: churn[:4]},
		"pods", "-w", "--sort", "-restarts")
	last := frames[len(frames)-1]
	if got := frameOrder(last, "api", "db", "web"); got != "db,api,web" {
		t.Fatalf("order %s, want db,api,web:\n%s", got, last)
	}
	want := map[string]string{"api": "1", "db": "2", "web": "3"}
	if got := numbersIn(last); !maps.Equal(got, want) {
		t.Errorf("numbers %v, want %v", got, want)
	}
}

// The watch saves what it numbered as the listing kx get would: every row it
// ever numbered, deleted ones included, so the nth entry is the row drawn as
// n, under the plain query, without the watch flags, so a refresh runs a
// listing that completes.
func TestWatchSavesItsNumberedRowsAsTheListing(t *testing.T) {
	_, services := watchTerminal(t, &fakeKubectl{namespace: "prod", watchLines: churn}, "pods", "-w")
	entry, err := services.State.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(entry.Names(), ","); got != "api,db,web,cache" {
		t.Errorf("saved rows %s, want api,db,web,cache", got)
	}
	if entry.Query == nil || entry.Query.Resource != "pods" || len(entry.Query.Args) != 0 {
		t.Errorf("saved query %+v, want plain pods with no args", entry.Query)
	}
	if entry.Namespace != "prod" || entry.Context != "test-context" {
		t.Errorf("saved namespace %q context %q, want prod, test-context", entry.Namespace, entry.Context)
	}
	name, namespace, kind, err := services.State.Fields(4)
	if err != nil || name != "cache" || namespace != "prod" || kind != kinds.Pod {
		t.Errorf("Fields(4) = %s %s %s %v, want the pod cache in prod", name, namespace, kind, err)
	}
}

// manualTicks replaces the watch's once-a-second ticker with one the test
// drives: the returned func ticks the running watch, synchronously, so a
// test can place a tick between two events exactly.
func manualTicks(t *testing.T) func() {
	t.Helper()
	var tick func()
	previous := watchTicker
	watchTicker = func(f func()) func() { tick = f; return func() {} }
	t.Cleanup(func() { watchTicker = previous })
	return func() { tick() }
}

// interleavedWatch runs kx get pods -w over lines on a terminal, calling
// between after the line at position at with the watch's services and its
// tick, and returns the frames drawn and the services.
func interleavedWatch(t *testing.T, lines []string, at int, between func(Services, func())) ([]string, Services) {
	t.Helper()
	tick := manualTicks(t)
	kube := interleavedKubectl{fakeKubectl: &fakeKubectl{namespace: "prod", watchLines: lines}, at: at}
	services := switchServices(t, kube)
	kube.between = func() { between(services, tick) }
	services.Kubectl = kube
	var term terminalBuffer
	render.SetOutput(&term, &bytes.Buffer{}, "github-dark")
	if err := executeArgv(newGetCommand(services), []string{"pods", "-w"}); err != nil {
		t.Fatal(err)
	}
	return framesOf(term.String()), services
}

// listServices saves a Service listing, as kx get svc in another terminal.
func listServices(t *testing.T, services Services) {
	t.Helper()
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"db"}, kinds.Service), Namespace: "prod",
		Query: &state.Query{Resource: "svc", Args: []string{}},
	}); err != nil {
		t.Fatal(err)
	}
}

// assertServicesCurrent fails unless the Service listing is still current.
func assertServicesCurrent(t *testing.T, services Services) {
	t.Helper()
	entry, err := services.State.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entry.Names(), ","); got != "db" || entry.Query.Resource != "svc" {
		t.Errorf("current listing %s of %s, want the svc listing left alone", got, entry.Query.Resource)
	}
}

// Once another terminal lists something else, the watch's next save does not
// land, and the watch says so and drops its numbers: kx describe 1 there
// would otherwise describe the watch's pod instead of the Service listed
// last. The uninterrupted watch (TestWatchSavesItsNumberedRowsAsTheListing)
// is the control, saving all four.
func TestWatchStopsSavingOnceAnotherListingIsCurrent(t *testing.T) {
	frames, services := interleavedWatch(t, churn, 3, func(services Services, tick func()) {
		tick()
		listServices(t, services)
	})
	assertServicesCurrent(t, services)
	last := frames[len(frames)-1]
	if !strings.Contains(last, supersededWatchNote) {
		t.Errorf("last frame has no %q note:\n%s", supersededWatchNote, last)
	}
	if got := numbersIn(last); len(got) != 0 {
		t.Errorf("last frame still numbers rows %v:\n%s", got, last)
	}
}

// A quiet watch notices too: after the other listing, its rows only change
// state, so no save ever runs, and only the tick's check can drop numbers
// that now resolve against the Service listing. The frame asserted on is
// the one the MODIFIED line draws, before the watch ends.
func TestQuietWatchDropsItsNumbersOnceAnotherListingIsCurrent(t *testing.T) {
	lines := []string{
		watchHeader,
		"ADDED      api    1/1     Running            2          1d",
		"ADDED      db     0/1     CrashLoopBackOff   14         1d",
		"MODIFIED   api    1/1     Running            3          1d",
	}
	frames, services := interleavedWatch(t, lines, 2, func(services Services, tick func()) {
		tick()
		listServices(t, services)
		tick()
	})
	assertServicesCurrent(t, services)
	modified := frames[len(frames)-1]
	if !regexp.MustCompile(`api\s+1/1\s+Running\s+3\s`).MatchString(modified) {
		t.Fatalf("last frame is not the MODIFIED one:\n%s", modified)
	}
	if !strings.Contains(modified, supersededWatchNote) || len(numbersIn(modified)) != 0 {
		t.Errorf("MODIFIED frame still numbers its rows:\n%s", modified)
	}
}

// A re-run of the watch's own query — the refresh a deleted row's index
// triggers, or kx get pods in another terminal — renumbers the rows, and the
// note names that rather than "a newer listing".
func TestWatchNamesARerunOfItsOwnListing(t *testing.T) {
	frames, _ := interleavedWatch(t, churn[:3], 2, func(services Services, tick func()) {
		tick()
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"db", "api"}, kinds.Pod), Namespace: "prod",
			Query: &state.Query{Resource: "pods", Args: []string{}}, Context: "test-context",
		}); err != nil {
			t.Fatal(err)
		}
		tick()
	})
	if last := frames[len(frames)-1]; !strings.Contains(last, relistedWatchNote) {
		t.Errorf("last frame has no %q note:\n%s", relistedWatchNote, last)
	}
}

// A burst of new rows is saved on the tick after it, in one write, not one
// write per row: after three rows and no tick nothing is saved yet. The tick
// is the control — after it the burst must be there, whole.
func TestWatchSavesABurstOfRowsOnTheNextTick(t *testing.T) {
	interleavedWatch(t, churn[:5], 3, func(services Services, tick func()) {
		if _, err := services.State.Load(); !errors.Is(err, state.ErrNoState) {
			t.Errorf("rows were saved before a tick: %v", err)
		}
		tick()
		entry, err := services.State.Load()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(entry.Names(), ","); got != "api,db,web" {
			t.Errorf("tick saved %s, want the burst api,db,web", got)
		}
	})
}

// A state file kx cannot write ends the numbering, not the watch: before
// watches were numbered one never wrote it. The watch still draws every row.
func TestWatchKeepsWatchingWhenItCannotSave(t *testing.T) {
	tick := manualTicks(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	kube := interleavedKubectl{fakeKubectl: &fakeKubectl{namespace: "prod", watchLines: churn[:4]}, at: 1}
	kube.between = func() { tick() }
	services := switchServices(t, kube)
	services.State.Path = filepath.Join(blocker, "state.json")
	var term terminalBuffer
	render.SetOutput(&term, &bytes.Buffer{}, "github-dark")
	if err := executeArgv(newGetCommand(services), []string{"pods", "-w"}); err != nil {
		t.Fatalf("watch ended on the failed save: %v", err)
	}
	frames := framesOf(term.String())
	last := frames[len(frames)-1]
	if !strings.Contains(last, unkeptWatchNote) || !strings.Contains(last, "web ") || len(numbersIn(last)) != 0 {
		t.Errorf("last frame should draw every row unnumbered under %q:\n%s", unkeptWatchNote, last)
	}
}

// Past maxWatchIndexes a new row is drawn without a number, and no number
// already given changes: the saved listing stops growing.
func TestWatchStopsNumberingAtItsLimit(t *testing.T) {
	previous := maxWatchIndexes
	maxWatchIndexes = 2
	t.Cleanup(func() { maxWatchIndexes = previous })
	frames, services := watchTerminal(t, &fakeKubectl{namespace: "prod", watchLines: churn[:4]}, "pods", "-w")
	last := frames[len(frames)-1]
	if got, want := numbersIn(last), map[string]string{"api": "1", "db": "2"}; !maps.Equal(got, want) {
		t.Errorf("numbers %v, want %v:\n%s", got, want, last)
	}
	if !strings.Contains(last, "web ") || !strings.Contains(last, fullWatchNote(2)) {
		t.Errorf("last frame should draw web unnumbered under %q:\n%s", fullWatchNote(2), last)
	}
	if entry, err := services.State.Load(); err != nil || strings.Join(entry.Names(), ",") != "api,db" {
		t.Errorf("saved %v (%v), want api,db", entry.Names(), err)
	}
}

// A watch of indexes saves its listing as kx get of them does: under the
// listing's namespace, with no -n the user never typed in the query.
func TestWatchOfIndexesKeepsTheScopeOutOfItsQuery(t *testing.T) {
	kube := &fakeKubectl{namespace: "default", watchLines: churn[:2]}
	services := switchServices(t, kube)
	if err := services.State.Save(state.State{
		Resources: state.NewResources([]string{"api"}, kinds.Pod), Namespace: "prod",
		Query: &state.Query{Resource: "pods", Args: []string{"-n", "prod"}},
	}); err != nil {
		t.Fatal(err)
	}
	var term terminalBuffer
	render.SetOutput(&term, &bytes.Buffer{}, "github-dark")
	if err := executeArgv(newGetCommand(services), []string{"pods", "1", "-w"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(kube.watchArgs, "prod") {
		t.Fatalf("kubectl watched %v, want it under -n prod", kube.watchArgs)
	}
	entry, err := services.State.Load()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Namespace != "prod" || slices.Contains(entry.Query.Args, "-n") {
		t.Errorf("saved namespace %q args %v, want prod with no -n", entry.Namespace, entry.Query.Args)
	}
}

// Off a terminal nothing is drawn, so nothing is numbered or saved; another
// cluster's watch is drawn but never numbered, as its listing never is. The
// terminal watch of this cluster is the control, saving a listing.
func TestWatchSavesNothingItDoesNotNumber(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		argv     []string
		saves    bool
		note     string
	}{
		{"terminal", true, []string{"pods", "-w"}, true, numberedWatchNote},
		{"off a terminal", false, []string{"pods", "-w"}, false, watchNote},
		{"another cluster", true, []string{"pods", "-w", "--context=b"}, false, crossClusterWatchNote},
	} {
		services := switchServices(t, &fakeKubectl{namespace: "prod", watchLines: churn[:3]})
		var out bytes.Buffer
		if tc.terminal {
			term := &terminalBuffer{}
			render.SetOutput(term, &bytes.Buffer{}, "github-dark")
			if err := executeArgv(newGetCommand(services), tc.argv); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			out.Write(term.Bytes())
		} else {
			render.SetOutput(&out, &bytes.Buffer{}, "github-dark")
			if err := executeArgv(newGetCommand(services), tc.argv); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
		_, err := services.State.Load()
		if saved := err == nil; saved != tc.saves {
			t.Errorf("%s: saved = %v (%v), want %v", tc.name, saved, err, tc.saves)
		}
		if !tc.saves && err != nil && !errors.Is(err, state.ErrNoState) {
			t.Errorf("%s: Load error %v, want no state", tc.name, err)
		}
		if !strings.Contains(out.String(), tc.note) {
			t.Errorf("%s: output has no %q:\n%s", tc.name, tc.note, out.String())
		}
	}
}

// kx state drop --all in another terminal clears the listings; that is not
// another listing replacing the watch's, and the note says which it was.
// The control is TestQuietWatchDropsItsNumbersOnceAnotherListingIsCurrent,
// where a listing did replace it.
func TestWatchSaysWhenTheSavedListingsWereCleared(t *testing.T) {
	frames, _ := interleavedWatch(t, churn[:4], 2, func(services Services, tick func()) {
		tick()
		if err := os.Remove(services.State.Path); err != nil {
			t.Fatal(err)
		}
		tick()
	})
	if last := frames[len(frames)-1]; !strings.Contains(last, clearedWatchNote) || len(numbersIn(last)) != 0 {
		t.Errorf("last frame should drop its numbers under %q:\n%s", clearedWatchNote, last)
	}
}

// Every footer a watch draws fits an 80-column terminal on one line, with the column after it to spare: some terminals wrap a line that fills the width exactly. The
// redraw counts a wrapped footer's lines too (render), but a footer that
// wraps at the commonest width is still one that reads badly.
func TestWatchFootersFitEightyColumns(t *testing.T) {
	for _, note := range []string{
		watchNote, numberedWatchNote, supersededWatchNote, relistedWatchNote,
		clearedWatchNote, unkeptWatchNote, crossClusterWatchNote, fullWatchNote(maxWatchIndexes),
	} {
		if n := utf8.RuneCountInString(note); n > 79 {
			t.Errorf("%d columns: %q", n, note)
		}
	}
}

// Stopping the ticker waits for a tick already running, so no tick draws or
// saves after the watch has returned. The real ticker, so this waits about a
// second for its first tick. The control is the tick being released: stop
// must then return.
func TestWatchTickerStopWaitsForARunningTick(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stop := watchTicker(func() {
		once.Do(func() { close(entered) })
		<-release
	})
	<-entered
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while a tick was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop never returned after the tick finished")
	}
}
