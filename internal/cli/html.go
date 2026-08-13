package cli

import (
	"context"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/theme"
	"github.com/jzills/kx/internal/web"
)

// htmlOptions are the three flags shared by the commands that can render HTML.
type htmlOptions struct {
	Enabled bool
	Port    int
	NoOpen  bool
}

// pageMeta builds the provenance block every page carries.
//
// title is the browser tab's, and starts at the command rather than at "kx" —
// "diag · prod", not "kx diag · prod". A tab shows the favicon first and then
// as much of the title as it has room for, and the favicon is already the kx
// mark; spending the first three characters saying so again pushed the part
// that tells two tabs apart out of view.
//
// An empty theme name falls back to the default rather than erroring: Services
// is constructed literally in several tests, and a zero Config there should
// not turn into a failed render.
func pageMeta(themeName, title, invocation string) (web.Meta, error) {
	if themeName == "" {
		themeName = theme.Default
	}
	styles, err := theme.WebStyles(themeName)
	if err != nil {
		return web.Meta{}, err
	}
	return web.Meta{
		Title:      title,
		Invocation: invocation,
		Captured:   time.Now(),
		Styles:     styles,
	}, nil
}

// servePage hands a rendered page to a browser and blocks until Ctrl-C.
//
// Ctrl-C is how this command is meant to end, so it returns nil rather than an
// error: the user stopping a server they asked for is not a failure.
func servePage(ctx context.Context, page []byte, opts htmlOptions) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	open := web.OpenBrowser
	if opts.NoOpen {
		open = nil
	}
	return web.Serve(ctx, page, web.Options{
		Port: opts.Port,
		Open: open,
		Announce: func(url string) {
			render.Caption("serving at "+url, "Ctrl-C to stop")
		},
	})
}

// invocation reconstructs the command line for the page's provenance line.
func invocation(command string, parts ...string) string {
	line := "kx " + command
	for _, part := range parts {
		if part != "" {
			line += " " + part
		}
	}
	return line
}

// portFlag renders a port for the invocation line.
func portFlag(port int) string {
	if port == 0 {
		return ""
	}
	return "--port " + strconv.Itoa(port)
}

// scopeArgs renders the scope flags for the invocation line, so the page says
// which namespaces it covers.
func scopeArgs(namespace string, allNamespaces bool) string {
	if allNamespaces {
		return "-A"
	}
	if namespace != "" {
		return "-n " + namespace
	}
	return ""
}
