package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// serveForTest starts Serve in the background and returns the announced URL.
func serveForTest(t *testing.T, page []byte, open func(string) error) (string, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	done := make(chan error, 1)

	go func() {
		done <- Serve(ctx, page, Options{
			Open:     open,
			Announce: func(url string) { urls <- url },
		})
	}()

	select {
	case url := <-urls:
		return url, cancel, done
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Serve never announced a URL")
		return "", cancel, done
	}
}

func TestServeHandsOutThePage(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("<!doctype html><p>hi</p>"), nil)
	defer func() { cancel(); <-done }()

	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s returned %v", url, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading body returned %v", err)
	}
	if !strings.Contains(string(body), "<p>hi</p>") {
		t.Errorf("body = %q", body)
	}
}

// The page carries cluster data; it must never leave the loopback interface.
func TestServeBindsLoopbackOnly(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("x"), nil)
	defer func() { cancel(); <-done }()

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("served at %q, want a 127.0.0.1 address", url)
	}
}

// Every path answers with the same document: there is one page and nothing to
// route, so a stray path must not 404.
func TestServeAnswersEveryPath(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("only-page"), nil)
	defer func() { cancel(); <-done }()

	response, err := http.Get(url + "/anything/at/all")
	if err != nil {
		t.Fatalf("GET returned %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
}

// Loopback binding alone does not stop DNS rebinding: a hostname an attacker
// controls can resolve to 127.0.0.1, making requests to it same-origin with
// this page even though it carries pod logs, event messages and CVE
// inventories. Only the Host actually bound may be served.
func TestServeRejectsAnUnexpectedHost(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("secret"), nil)
	defer func() { cancel(); <-done }()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest returned %v", err)
	}
	// Request.Host is what actually governs the wire request; setting the
	// "Host" header via req.Header.Set has no effect — the transport always
	// sends req.Host (or, if empty, the URL's host) as the header line.
	req.Host = "attacker.example:1"

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with a foreign Host returned %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for an unexpected Host", response.StatusCode)
	}

	// The check must not be so broad it rejects everyone: a plain request,
	// whose Host is whatever was actually bound, still gets the page.
	ok, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s returned %v", url, err)
	}
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for the bound Host", ok.StatusCode)
	}
}

// A browser that will not launch is not a reason to stop serving.
func TestServeSurvivesAFailedBrowserOpen(t *testing.T) {
	opened := make(chan string, 1)
	url, cancel, done := serveForTest(t, []byte("still-here"), func(u string) error {
		opened <- u
		return errors.New("no browser here")
	})
	defer func() { cancel(); <-done }()

	select {
	case got := <-opened:
		if got != url {
			t.Errorf("opened %q, announced %q", got, url)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open was never called")
	}

	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET after a failed open returned %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", response.StatusCode)
	}
}

func TestServeShutsDownOnContextCancel(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("bye"), nil)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}

	if _, err := http.Get(url); err == nil {
		t.Error("the server is still answering after shutdown")
	}
}

// A connection a browser opened but never sent a request on — a preconnect —
// cannot be drained inside the 2s shutdown grace: net/http only treats a
// brand-new connection as idle once it has sat unused for 5s (see
// closeIdleConnsLocked in net/http's server.go), so Shutdown's poll loop is
// still waiting when its own deadline expires. An ordinary Ctrl-C ending that
// way is a clean stop, not an error — the spec says Ctrl-C exits 0.
func TestServeTreatsAnUnusedConnectionAsACleanStop(t *testing.T) {
	url, cancel, done := serveForTest(t, []byte("hi"), nil)

	host := strings.TrimPrefix(url, "http://")
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial %s returned %v", host, err)
	}
	defer conn.Close()
	// Deliberately sending nothing: the connection sits in net/http's
	// StateNew, unread, exactly like a browser's idle preconnect.

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil — a stuck preconnect must not fail an ordinary shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

// An explicitly chosen port that is already taken has to say so by number.
//
// The busy port is pulled from a live server's own announced URL via
// net/url rather than by hand-indexing the string: Serve does not return its
// listener, and url.Parse's Port() is the robust way to recover a port from
// a URL without assuming where the colons fall.
//
// The assertion checks for the "cannot serve on port N" wrapping, not just
// the bare number: the raw net.Listen error is "listen tcp 127.0.0.1:N:
// bind: address already in use", which already contains N, so a
// strings.Contains on the number alone would keep passing even if server.go's
// wrap were deleted — it would no longer be testing the regression this test
// exists to catch.
//
// The context carries its own 5s timeout rather than a bare
// context.WithCancel: every other test in this file bounds its waits the
// same way. If Serve ever stopped honouring opts.Port and bound an ephemeral
// port instead, the second Serve call below would succeed and block forever
// in its own select — this timeout is what turns that into a reported test
// failure instead of a 10-minute panic.
func TestServeReportsABusyPort(t *testing.T) {
	served, cancel, done := serveForTest(t, []byte("first"), nil)
	defer func() { cancel(); <-done }()

	taken, err := portOf(served)
	if err != nil {
		t.Fatalf("could not parse port from %q: %v", served, err)
	}

	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err = Serve(ctx, []byte("second"), Options{Port: taken})
	if err == nil {
		t.Fatal("serving on a taken port returned no error")
	}
	want := "cannot serve on port " + strconv.Itoa(taken)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

// portOf recovers the numeric port from a served URL such as
// "http://127.0.0.1:54321".
func portOf(rawURL string) (int, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(parsed.Port())
}
