package render

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jzills/kx/internal/theme"
)

// How long a command runs silently before the spinner appears. Quick commands
// intentionally show nothing — a spinner that flashes for a fraction of a
// second is noise, not feedback.
const spinnerDelay = 200 * time.Millisecond

// Frame cadence chosen so the line isn't rewritten faster than the animation
// advances, which flickers on some terminals.
const spinnerInterval = 250 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Status shows a spinner while a command waits on the cluster, and returns the
// function that stops it.
//
// A no-op off-terminal, so piped output and test captures never receive
// spinner frames. Frames go to stderr so a spinner can never land in the middle
// of output being piped to another program.
func (r *Renderer) Status(message string) (stop func()) {
	if !isTerminal(r.out) {
		return func() {}
	}

	var (
		mu       sync.Mutex
		started  bool
		finished bool
		done     = make(chan struct{})
	)

	clear := func() {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	timer := time.AfterFunc(spinnerDelay, func() {
		mu.Lock()
		if finished {
			mu.Unlock()
			return
		}
		started = true
		mu.Unlock()

		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		frame := 0
		for {
			// Paint immediately: the delay has already passed, so waiting a
			// full interval for the first frame would show nothing for 450ms.
			mu.Lock()
			if finished {
				mu.Unlock()
				return
			}
			fmt.Fprint(os.Stderr, "\r"+r.style(theme.Muted, spinnerFrames[frame]+" "+message+"…"))
			mu.Unlock()

			frame = (frame + 1) % len(spinnerFrames)
			select {
			case <-ticker.C:
			case <-done:
				return
			}
		}
	})

	return func() {
		timer.Stop()
		// The lock closes the race where the timer has fired but the first
		// frame has not been painted, which would otherwise leave the spinner
		// running after the command finished.
		mu.Lock()
		wasStarted := started
		finished = true
		mu.Unlock()
		close(done)
		if wasStarted {
			clear()
		}
	}
}

// Banner is the context line above a resource's output: "Pod/nginx · prod".
func (r *Renderer) Banner(kind, name, namespace, extra string) {
	r.Caption(kind+"/"+name, namespace, extra)
}

// ScopeBanner is the caption for a cross-kind sweep, matching kx diag's
// "Mixed · …" header for namespace-spanning listings.
func (r *Renderer) ScopeBanner(label, namespace, extra string) {
	r.Caption(label, namespace, extra)
}

// Blank prints an empty line, used to separate repeated blocks when a command
// is given several indexes.
func (r *Renderer) Blank() { fmt.Fprintln(r.out) }

// emphasizePaths accents kind/name tokens in a prompt, matching banners.
func (r *Renderer) emphasizePaths(message string) string {
	words := strings.Fields(message)
	for i, word := range words {
		if strings.Contains(word, "/") {
			words[i] = r.style(theme.Accent, word)
			continue
		}
		words[i] = r.style(theme.Body, word)
	}
	return strings.Join(words, " ")
}

func Status(message string) func()               { return current.Status(message) }
func Banner(kind, name, namespace, extra string) { current.Banner(kind, name, namespace, extra) }
func ScopeBanner(label, namespace, extra string) { current.ScopeBanner(label, namespace, extra) }
func Blank()                                     { current.Blank() }
