package cli

import (
	"bytes"
	"errors"
	"maps"
	"regexp"
	"strings"
	"testing"

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

// Once another terminal lists something else, the watch stops saving and
// says so, rather than push its rows back over that listing: kx describe 1
// there would otherwise describe the watch's pod instead of the Service
// listed last. The uninterrupted watch above is the control, saving all four.
func TestWatchStopsSavingOnceAnotherListingIsCurrent(t *testing.T) {
	kube := interleavedKubectl{fakeKubectl: &fakeKubectl{namespace: "prod", watchLines: churn}, at: 3}
	services := switchServices(t, kube)
	kube.between = func() {
		if err := services.State.Save(state.State{
			Resources: state.NewResources([]string{"db"}, kinds.Service), Namespace: "prod",
			Query: &state.Query{Resource: "svc", Args: []string{}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	services.Kubectl = kube
	var term terminalBuffer
	render.SetOutput(&term, &bytes.Buffer{}, "github-dark")
	if err := executeArgv(newGetCommand(services), []string{"pods", "-w"}); err != nil {
		t.Fatal(err)
	}
	frames := framesOf(term.String())

	entry, err := services.State.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entry.Names(), ","); got != "db" || entry.Query.Resource != "svc" {
		t.Errorf("current listing %s of %s, want the svc listing left alone", got, entry.Query.Resource)
	}
	last := frames[len(frames)-1]
	if !strings.Contains(last, supersededWatchNote) {
		t.Errorf("last frame has no %q note:\n%s", supersededWatchNote, last)
	}
	if got := numbersIn(last); len(got) != 0 {
		t.Errorf("last frame still numbers rows %v:\n%s", got, last)
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
		{"another cluster", true, []string{"pods", "-w", "--context=b"}, false, crossClusterWatchNote("--context")},
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
