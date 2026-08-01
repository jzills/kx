package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Options configures a serve. The two callbacks are injected rather than
// called directly so a test can serve a page without opening a browser or
// writing to the terminal.
type Options struct {
	// Port is the loopback port to bind. Zero takes an ephemeral one, which
	// cannot collide with anything.
	Port int
	// Open launches a browser. A nil Open skips it, which is what --no-open
	// and every test want.
	Open func(string) error
	// Announce reports the URL. Called before Open, so a browser that fails to
	// launch still leaves something usable on screen.
	Announce func(string)
}

// shutdownGrace is how long an in-flight response gets to finish after Ctrl-C.
// The page is a single small document, so this is generous.
const shutdownGrace = 2 * time.Second

// Serve hands out one already-rendered page until ctx is cancelled.
//
// It binds 127.0.0.1 and nothing else: the page carries pod logs, event
// messages and vulnerability inventories, none of which belong on a network
// interface.
func Serve(ctx context.Context, page []byte, opts Options) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		if opts.Port != 0 {
			return fmt.Errorf("cannot serve on port %d: %w", opts.Port, err)
		}
		return err
	}

	url := "http://" + listener.Addr().String()
	server := &http.Server{Handler: pageHandler(page)}

	failed := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
			return
		}
		failed <- nil
	}()

	if opts.Announce != nil {
		opts.Announce(url)
	}
	if opts.Open != nil {
		// A browser that will not open is not a reason to stop serving — the
		// URL has already been announced and can be pasted.
		if err := opts.Open(url); err != nil {
			openFailed(err)
		}
	}

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	stop, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return server.Shutdown(stop)
}

// pageHandler answers every path with the same document. There is one page, so
// there is nothing to route.
func pageHandler(page []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Nothing is cached: a reload should re-fetch, not resurrect a page
		// from a previous run on the same ephemeral port.
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write(page)
	})
}
