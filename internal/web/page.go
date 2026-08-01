// Package web renders kx's analysis output as a themed HTML page and serves it
// from a loopback port.
//
// Rendering and serving are separate on purpose: the Render functions are pure
// functions over the same values the terminal renderer consumes, so a page can
// be tested by comparing bytes without a socket or a browser.
//
// internal/render knows nothing about this package, and this package adds no
// rendering to it — it borrows two classifications so the page and the
// terminal cannot disagree about severity.
package web

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/scanner"
)

// Meta is the provenance every page carries: what was run, when, and where it
// is being served.
//
// Named Meta rather than Chrome because theme.Chrome is the palette's page
// colours, and two unrelated Chromes one import apart would be a trap.
type Meta struct {
	Title      string
	Invocation string
	Captured   time.Time
	URL        string
	// Styles is theme.WebStyles' output for the active palette.
	Styles map[string]string
}

// DiagPage covers both diagnostic shapes. A single-resource report is a sweep
// of one with Single set: the template renders the same report block either
// way, inline or inside a <details>, which is what stops the two views
// drifting apart.
type DiagPage struct {
	Meta
	// Scope is the namespace, or "all namespaces".
	Scope         string
	AllNamespaces bool
	Single        bool
	Checked       int
	Healthy       int
	// Reports are the unhealthy resources, most severe first — or exactly one
	// resource when Single is set, healthy or not.
	Reports []diagnostics.Report
	Dropped []string
}

// ScanPage is one image-scan sweep.
type ScanPage struct {
	Meta
	Scope  string
	Images []scanner.ImageScan
}

// Usage is a container's consumption of one resource against its limit.
//
// Known is false when no limit is set, which the page draws as an em dash.
// "No limit configured" and "using none of its limit" are different facts and
// must not both render as 0%.
type Usage struct {
	Known bool
	Pct   int
	// Class is the CSS class for the severity of this percentage, or "" when
	// the percentage warrants no styling.
	Class string
}

// usageOf converts a usage/limit pair into a drawable percentage.
//
// MilliValue is used for both CPU and memory: it is exact for CPU, and for
// memory a byte count multiplied by 1000 stays well inside int64 for any
// limit a container can actually carry.
func usageOf(used, limit *resource.Quantity, kind string) Usage {
	if used == nil || limit == nil || limit.IsZero() {
		return Usage{}
	}
	pct := int(float64(used.MilliValue()) / float64(limit.MilliValue()) * 100)
	return Usage{Known: true, Pct: pct, Class: styleClass(render.UsageStyle(pct, kind))}
}

// styleClass turns a semantic style name into a CSS class ("status.ok" →
// "status-ok"), so the palette's vocabulary reaches the stylesheet unchanged.
// An empty name yields an empty class rather than a stray "-".
func styleClass(name string) string {
	if name == "" {
		return ""
	}
	return strings.ReplaceAll(name, ".", "-")
}

func severityClass(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return "status-bad"
	case diagnostics.Warning:
		return "status-warn"
	default:
		return "status-ok"
	}
}

// severityIcon matches the terminal's markers exactly, so a screenshot of one
// reads the same as the other.
func severityIcon(severity diagnostics.Severity) string {
	switch severity {
	case diagnostics.Critical:
		return "✗"
	case diagnostics.Warning:
		return "!"
	default:
		return "✓"
	}
}

// cssVars renders the palette as custom-property declarations.
//
// This is the one place page content becomes template.CSS, and it is safe
// because the values never come from page data: theme.WebStyles returns only
// #rrggbb, guarded by a test over every registered theme. Do not generalise
// from this — nothing else in this package may be marked pre-escaped.
func cssVars(styles map[string]string) template.CSS {
	var out strings.Builder
	out.WriteString(":root{")
	// Sorted so the output is byte-stable for golden-file tests; Go map
	// iteration order is randomised.
	for _, name := range sortedKeys(styles) {
		fmt.Fprintf(&out, "--%s:%s;", styleClass(name), styles[name])
	}
	out.WriteString("}")
	return template.CSS(out.String())
}

func sortedKeys(styles map[string]string) []string {
	names := make([]string, 0, len(styles))
	for name := range styles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// funcs are the derivations templates cannot express.
var funcs = template.FuncMap{
	"cssVars":       cssVars,
	"stylesheet":    func() template.CSS { return template.CSS(stylesheet) },
	"statusClass":   func(status string) string { return styleClass(render.StatusStyle(status)) },
	"severityClass": severityClass,
	"severityIcon":  severityIcon,
	// age is a stub so the templates parse; RenderDiag and RenderScan rebind
	// it per call to a closure over the page's own Captured time, so the same
	// page value always renders the same bytes rather than reading the clock.
	"age": func(time.Time) string { return "" },
	"cpuUsage": func(c diagnostics.ContainerDiagnostic) Usage {
		return usageOf(c.CPUUsage, c.CPULimit, "cpu")
	},
	"memoryUsage": func(c diagnostics.ContainerDiagnostic) Usage {
		return usageOf(c.MemoryUsage, c.MemoryLimit, "memory")
	},
	"ready": func(pod diagnostics.PodDiagnostic) string {
		return fmt.Sprintf("%d/%d", pod.ReadyContainers, pod.TotalContainers)
	},
	// reason is whichever of the two the container actually has; the terminal
	// table collapses them into one column the same way.
	"reason": func(c diagnostics.ContainerDiagnostic) string {
		if c.WaitingReason != "" {
			return c.WaitingReason
		}
		return c.TerminatedReason
	},
}
