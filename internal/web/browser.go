package web

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/jzills/kx/internal/render"
)

// openers are the browser-launch commands to try, in order, for the running
// platform.
//
// The Linux chain is ordered for WSL, where xdg-open is frequently absent:
// wslview and explorer.exe both hand the URL to the Windows default browser.
// A loopback URL survives that boundary, which a file:// path would not — one
// of the reasons this feature serves a page rather than writing one.
func openers(url string) [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"open", url}}
	case "windows":
		return [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	default:
		return [][]string{{"xdg-open", url}, {"wslview", url}, {"explorer.exe", url}}
	}
}

// OpenBrowser launches the platform's default browser at url, trying each
// opener until one succeeds.
func OpenBrowser(url string) error {
	var last error
	for _, argv := range openers(url) {
		cmd := exec.Command(argv[0], argv[1:]...)
		if err := cmd.Start(); err != nil {
			last = err
			continue
		}
		// Not waited on: a browser process outlives kx, and explorer.exe exits
		// non-zero even when it has successfully handed the URL over.
		return nil
	}
	if last == nil {
		last = fmt.Errorf("no browser opener found")
	}
	return last
}

// openFailed reports a browser that would not launch. Not fatal: the URL was
// announced first precisely so this case degrades to copy-and-paste.
func openFailed(err error) {
	render.Error(fmt.Sprintf("could not open a browser (%v) — open the URL above instead.", err))
}
