package cli

import (
	"context"
	"fmt"
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

// validate refuses the flags that only configure --html's server when no HTML
// was asked for.
//
// Both were accepted and dropped on the floor: `kx diag --port 9090` printed a
// table and said nothing about the port it had ignored. kx refuses every other
// contradictory combination it can see — '--json' with '--html', '--full' with
// '--fail-on', a scope flag beside an index — and a flag that configures a
// server nobody asked to start is the same mistake, told the same way.
//
// portSet and noOpenSet are passed rather than read off the struct because
// zero is a legitimate --port (it means "pick a free one"), so the value
// cannot say whether the flag was given.
func (o htmlOptions) validate(portSet, noOpenSet bool) error {
	if o.Enabled {
		return nil
	}
	if portSet {
		return htmlOnlyFlagError("--port")
	}
	if noOpenSet {
		return htmlOnlyFlagError("--no-open")
	}
	return nil
}

func htmlOnlyFlagError(flag string) error {
	return fmt.Errorf(
		"'%s' only applies with '--html' — it configures the report's server, "+
			"and without '--html' there is nothing being served. Add '--html', "+
			"or drop '%s'.", flag, flag)
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
