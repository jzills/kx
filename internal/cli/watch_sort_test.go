package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/render"
)

// watchFrames runs kx get with the given argv against a watch that prints
// lines, on a renderer that takes its output for a terminal, and returns
// each frame the live table drew: the redraw clears the frame before it with
// an escape sequence, so a frame is what follows one.
func watchFrames(t *testing.T, argv []string, lines ...string) ([]string, error) {
	t.Helper()
	kube := &fakeKubectl{namespace: "prod", watchLines: lines}
	services := switchServices(t, kube)
	var out terminalBuffer
	var errOut bytes.Buffer
	render.SetOutput(&out, &errOut, "github-dark")
	err := executeArgv(newGetCommand(services), argv)
	var frames []string
	for _, frame := range strings.Split(out.String(), "\x1b[J") {
		if strings.Contains(frame, "NAME") {
			frames = append(frames, frame)
		}
	}
	return frames, err
}

// frameOrder is the order the given names appear in one frame.
func frameOrder(frame string, names ...string) string {
	ordered := slices.Clone(names)
	slices.SortFunc(ordered, func(a, b string) int {
		return strings.Index(frame, a+" ") - strings.Index(frame, b+" ")
	})
	return strings.Join(ordered, ",")
}

// watchHeader pads EVENT as kubectl does, wide enough for MODIFIED.
const watchHeader = "EVENT      NAME   READY   STATUS             RESTARTS   AGE"

// The live table a watch draws is sorted, as the listing it redraws would
// be. The unsorted watch is the control: its rows arrive in name order, so
// a sort that did nothing would draw them that way.
func TestWatchSortsTheLiveTable(t *testing.T) {
	lines := []string{
		watchHeader,
		"ADDED      api    1/1     Running            2          1d",
		"ADDED      db     0/1     CrashLoopBackOff   14         1d",
		"ADDED      web    1/1     Running            0          1d",
	}
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"pods", "-w"}, "api,db,web"},
		{[]string{"pods", "-w", "--sort", "-restarts"}, "db,api,web"},
		{[]string{"pods", "-w", "--sort", "status,-name"}, "db,web,api"},
	} {
		frames, err := watchFrames(t, tc.argv, lines...)
		if err != nil {
			t.Fatalf("%v: %v", tc.argv, err)
		}
		if len(frames) != 3 {
			t.Fatalf("%v: drew %d frames, want one per row", tc.argv, len(frames))
		}
		if got := frameOrder(frames[2], "api", "db", "web"); got != tc.want {
			t.Errorf("%v: last frame %s, want %s", tc.argv, got, tc.want)
		}
	}
}

// A row that changes moves to where its new cells sort: the sort runs on
// every redraw, not once over the rows a watch opened with.
func TestWatchSortMovesARowThatChanges(t *testing.T) {
	frames, err := watchFrames(t, []string{"pods", "-w", "--sort", "-restarts"},
		watchHeader,
		"ADDED      api    1/1     Running            0          1d",
		"ADDED      db     1/1     Running            1          1d",
		"MODIFIED   api    0/1     CrashLoopBackOff   5          1d",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("drew %d frames, want 3", len(frames))
	}
	if got := frameOrder(frames[1], "api", "db"); got != "db,api" {
		t.Errorf("before the change: %s, want db,api", got)
	}
	if got := frameOrder(frames[2], "api", "db"); got != "api,db" {
		t.Errorf("after api restarted five times: %s, want api,db", got)
	}
}

// A key naming no column of the watch's is refused on its header, before a
// row is drawn, in the sentence kx get gives a listing — and so even when no
// row ever arrives: a watch over an empty namespace would otherwise sit
// there with the misspelled key unremarked until something changed.
func TestWatchSortRefusesAColumnTheWatchHasNone(t *testing.T) {
	want := "Invalid value for '--sort': 'cpu'. Accepted values: name, ready, status, restarts, age."
	for name, lines := range map[string][]string{
		"with a row": {watchHeader, "ADDED      api    1/1     Running            0          1d"},
		"no rows":    {watchHeader},
	} {
		frames, err := watchFrames(t, []string{"pods", "-w", "--sort", "cpu"}, lines...)
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
		if len(frames) != 0 {
			t.Errorf("%s: drew %d frames before the refusal", name, len(frames))
		}
	}
}
