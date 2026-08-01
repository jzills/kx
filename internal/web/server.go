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

// readHeaderTimeout bounds how long a client gets to finish sending request
// headers. Good practice for any server accepting connections regardless, and
// it also keeps a slow-header client distinct from the no-request, browser-
// preconnect connections shutdownGrace has to tolerate below.
const readHeaderTimeout = 5 * time.Second

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

	addr := listener.Addr().String()
	url := "http://" + addr
	server := &http.Server{
		Handler:           pageHandler(page, addr),
		ReadHeaderTimeout: readHeaderTimeout,
	}

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
	if err := server.Shutdown(stop); err != nil {
		// A connection the browser opened and never used — a preconnect, say —
		// cannot be drained inside the grace window: net/http does not treat a
		// brand-new connection as idle until it has sat unused for 5s, well
		// past shutdownGrace. Forcing it closed is the right end for an
		// ordinary Ctrl-C, not an error to report — stopping a server you
		// asked to stop is not a failure.
		if errors.Is(err, context.DeadlineExceeded) {
			return server.Close()
		}
		return err
	}
	return nil
}

// pageHandler answers every path with the same document. There is one page, so
// there is nothing to route.
//
// The Host header is checked because binding loopback is not by itself
// enough: a hostname an attacker controls can resolve to 127.0.0.1, and
// requests to it are same-origin, so a page carrying pod logs and CVE
// inventories would be readable cross-site. Only the address we actually
// bound is accepted.
func pageHandler(page []byte, addr string) http.Handler {
	_, port, _ := net.SplitHostPort(addr)
	allowed := map[string]bool{
		"127.0.0.1:" + port: true,
		"localhost:" + port: true,
		"[::1]:" + port:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "unexpected Host", http.StatusForbidden)
			return
		}
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
