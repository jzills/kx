package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/theme"
	"github.com/jzills/kx/internal/web"
)

// htmlOptions are the flags shared by the commands that can render HTML.
//
// --html says produce a report; the rest say where it goes. By default that is
// a local server and a browser; --port picks the port, --no-open leaves the
// browser alone, and --out writes the page to a file instead of serving it at
// all.
type htmlOptions struct {
	Enabled bool
	Port    int
	NoOpen  bool
	// Out is a path to write the page to instead of serving it. Empty serves.
	Out string
}

// impliedHTML reports whether an HTML report was asked for — directly with
// --html, or implicitly by naming where to write one with --out. --out
// already says "HTML" in its own name and description ("Write the HTML
// report to this file..."), so `kx diag --out report.html` on its own is not
// a different request from `kx diag --html --out report.html` — it is the
// same request spelled with one flag instead of two, and every command reads
// html through this rather than the bare flag so the two spellings can never
// disagree about what they asked for.
func impliedHTML(html bool, out string) bool {
	return html || out != ""
}

// htmlFlagName names whichever flag actually asked for an HTML report, for a
// conflict error where that flag is on one side. Called only once
// impliedHTML(html, out) is already known true, so html itself decides it:
// --html directly, or --out when that was what implied it — never a flag the
// caller did not type.
func htmlFlagName(html bool) string {
	if html {
		return "--html"
	}
	return "--out"
}

// validate refuses the flags that only configure --html's server when no HTML
// was asked for — neither directly with --html nor implicitly with --out,
// which every caller is expected to have already folded into Enabled via
// impliedHTML by the time this runs.
//
// The Out-without-Enabled case stays checked even though it should be
// unreachable through that convention: refusing it loudly is what makes a
// caller that forgets impliedHTML fail a test immediately, rather than
// silently writing nothing — the exact "flag accepted and dropped on the
// floor" bug --port and --no-open are refused here for.
//
// portSet and noOpenSet are passed rather than read off the struct because
// zero is a legitimate --port (it means "pick a free one"), so the value
// cannot say whether the flag was given.
func (o htmlOptions) validate(portSet, noOpenSet bool) error {
	if !o.Enabled {
		switch {
		case portSet:
			return htmlOnlyFlagError("--port", "it configures the report's server")
		case noOpenSet:
			return htmlOnlyFlagError("--no-open", "it configures the report's server")
		case o.Out != "":
			return errors.New(
				"internal error: htmlOptions.Out is set without Enabled — the " +
					"caller must derive Enabled from impliedHTML(html, out).")
		}
		return nil
	}
	// --out replaces the server rather than configuring it, so the two flags
	// that configure one have nothing left to describe. Refused for the same
	// reason they are refused without --html at all: a flag that changes
	// nothing must not look as though it did.
	if o.Out == "" {
		return nil
	}
	switch {
	case portSet:
		return servedOnlyFlagError("--port")
	case noOpenSet:
		return servedOnlyFlagError("--no-open")
	}
	return nil
}

func servedOnlyFlagError(flag string) error {
	return fmt.Errorf(
		"'%s' cannot be combined with '--out' — '--out' writes the report to a "+
			"file instead of serving it, so there is no server for '%s' to "+
			"describe.", flag, flag)
}

// htmlOnlyFlagError refuses a flag that only means something once --html has
// asked for a report. role says what the flag does, since --port and --no-open
// configure the server while --out replaces it.
func htmlOnlyFlagError(flag, role string) error {
	return fmt.Errorf(
		"'%s' only applies with '--html' — %s, and without '--html' there is no "+
			"report. Add '--html', or drop '%s'.", flag, role, flag)
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

// deliverPage hands a rendered page to wherever the flags asked for it.
//
// A file with --out, and a browser otherwise. The distinction matters most in
// CI: serving blocks until Ctrl-C, which a pipeline never sends, so a job that
// wanted to publish a report and fail on it had no way to do the first without
// hanging on it. Writing returns, and the caller's --fail-on gate runs after.
func deliverPage(ctx context.Context, page []byte, opts htmlOptions) error {
	if opts.Out == "" {
		return servePage(ctx, page, opts)
	}
	// 0o644 rather than 0o600: this is a report meant to be picked up — by a
	// CI artifact step, a web server, a colleague — and it holds what kx
	// already printed to the terminal.
	if err := os.WriteFile(opts.Out, page, 0o644); err != nil {
		return err
	}
	render.Success("Wrote " + opts.Out)
	return nil
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
