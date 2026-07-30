package render

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jzills/kx/internal/theme"
	"github.com/muesli/termenv"
)

// syncBuffer is written by the spinner goroutine and read by the test, so it
// has to be safe for both. Without the lock the race detector would flag the
// test rather than the code under it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// spinnerRenderer builds a renderer whose error stream is a buffer the test can
// read. Status itself would never start on this renderer — stdout is not a
// terminal — which is exactly why status takes the check as a parameter.
func spinnerRenderer() (*Renderer, *syncBuffer) {
	errOut := &syncBuffer{}
	return newWithProfile(&bytes.Buffer{}, errOut, theme.Default, termenv.Ascii), errOut
}

// Off a terminal nothing is started and nothing is written, which is what keeps
// spinner frames out of piped output.
func TestStatusDisabledWritesNothing(t *testing.T) {
	r, errOut := spinnerRenderer()
	stop := r.status("fetching", false, time.Millisecond, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	stop()

	if got := errOut.String(); got != "" {
		t.Errorf("disabled spinner wrote %q", got)
	}
}

// A command that finishes inside the delay shows nothing. A spinner that
// flashes for a fraction of a second is noise, and this is the rule that keeps
// quick commands silent.
func TestStatusStoppedInsideTheDelayNeverPaints(t *testing.T) {
	r, errOut := spinnerRenderer()
	stop := r.status("fetching", true, time.Hour, time.Millisecond)
	stop()

	if got := errOut.String(); got != "" {
		t.Errorf("spinner painted before its delay elapsed: %q", got)
	}
}

// Once the delay passes a frame is painted, and stopping clears the line — a
// spinner left on screen would sit above the output that replaced it.
func TestStatusPaintsThenClears(t *testing.T) {
	r, errOut := spinnerRenderer()
	stop := r.status("fetching", true, time.Millisecond, 5*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(errOut.String(), "fetching") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	painted := errOut.String()
	if !strings.Contains(painted, "fetching") {
		t.Fatalf("spinner never painted within 2s: %q", painted)
	}
	if !strings.ContainsAny(painted, strings.Join(spinnerFrames, "")) {
		t.Errorf("painted output carries no spinner frame: %q", painted)
	}

	stop()
	if !strings.HasSuffix(errOut.String(), clearLine) {
		t.Errorf("stop did not clear the line, output ends %q",
			lastRunes(errOut.String(), 8))
	}
}

// Nothing calls stop twice today, and nothing stopped it either — a second
// close of the done channel would panic in the middle of a command.
func TestStatusStopIsIdempotent(t *testing.T) {
	r, _ := spinnerRenderer()
	stop := r.status("fetching", true, time.Millisecond, time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	stop()
	stop()
	stop()
}

// The race the stop path's lock exists to close: the timer has fired but the
// first frame has not been painted yet. Running many short-lived spinners at
// staggered delays lands stop() in that window, and -race is what makes the
// failure visible rather than intermittent.
func TestStatusStopRacesTheFirstFrame(t *testing.T) {
	for i := range 200 {
		r, _ := spinnerRenderer()
		stop := r.status("fetching", true, time.Duration(i%5)*time.Microsecond, time.Millisecond)
		stop()
	}
}

// Concurrent stops from several goroutines, which sync.Once has to serialise.
func TestStatusConcurrentStops(t *testing.T) {
	r, _ := spinnerRenderer()
	stop := r.status("fetching", true, time.Millisecond, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stop()
		}()
	}
	wg.Wait()
}

func lastRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
