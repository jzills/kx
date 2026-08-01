package web

import (
	"context"
	"errors"
	"io"
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

// An explicitly chosen port that is already taken has to say so by number.
//
// The busy port is pulled from a live server's own announced URL via
// net/url rather than by hand-indexing the string: Serve does not return its
// listener, and url.Parse's Port() is the robust way to recover a port from
// a URL without assuming where the colons fall.
func TestServeReportsABusyPort(t *testing.T) {
	served, cancel, done := serveForTest(t, []byte("first"), nil)
	defer func() { cancel(); <-done }()

	taken, err := portOf(served)
	if err != nil {
		t.Fatalf("could not parse port from %q: %v", served, err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	err = Serve(ctx, []byte("second"), Options{Port: taken})
	if err == nil {
		t.Fatal("serving on a taken port returned no error")
	}
	want := strconv.Itoa(taken)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the port %s", err, want)
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
