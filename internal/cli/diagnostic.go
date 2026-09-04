package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jzills/kx/internal/config"
	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/web"
	"github.com/spf13/cobra"
)

// Gatherer is the slice of the diagnostics service the commands need.
type Gatherer interface {
	Gather(ctx context.Context, kind kinds.Kind, name, namespace string) (diagnostics.Data, error)
	Sweep(ctx context.Context, namespace string) ([]diagnostics.Data, error)
}

// DiagnosticCommand analyses one indexed resource.
type DiagnosticCommand struct {
	State       IndexResolver
	Diagnostics Gatherer
}

func (c DiagnosticCommand) Execute(ctx context.Context, index int) (diagnostics.Report, error) {
	name, namespace, kind, err := c.State.Fields(index)
	if err != nil {
		return diagnostics.Report{}, err
	}
	if !diagnostics.SupportedKinds.Has(kind) {
		return diagnostics.Report{}, unsupportedKindError(
			"diagnostic", kind, diagnostics.SupportedKinds)
	}
	data, err := c.Diagnostics.Gather(ctx, kind, name, namespace)
	if err != nil {
		return diagnostics.Report{}, err
	}
	return diagnostics.BuildReport(data), nil
}

// TriageCommand sweeps a whole namespace.
type TriageCommand struct {
	Diagnostics Gatherer
	Save        func(state.State) error
}

// Execute sweeps one namespace, or every namespace when allNamespaces is set —
// which the sweep spells as an empty namespace, the way client-go's listers do.
// full mirrors kx diag --full: it decides only what the terminal table prints
// (every resource vs. unhealthy ones); the HTML report and the saved index
// state always cover every swept resource regardless of it, since the HTML
// grid filters healthy rows away client-side and an index must resolve
// whatever the grid can show.
//
// A cluster-wide sweep saves state like any other, with each resource
// recording the namespace it was swept from — the entry itself records none,
// since there is no single namespace the listing came from. kx get -A follows
// the same rule.
func (c TriageCommand) Execute(
	ctx context.Context, namespace string, allNamespaces, full bool,
) (render.TriageResult, error) {
	if allNamespaces {
		namespace = ""
	}
	all, err := c.Diagnostics.Sweep(ctx, namespace)
	if err != nil {
		return render.TriageResult{}, err
	}

	reports := make([]diagnostics.Report, 0, len(all))
	for _, data := range all {
		reports = append(reports, diagnostics.BuildReport(data))
	}
	// Most severe first, stable so the sweep's order survives within a
	// severity; healthy resources sort last since OK is the lowest Severity.
	sort.SliceStable(reports, func(i, j int) bool {
		return reports[i].Verdict > reports[j].Verdict
	})

	var unhealthy []diagnostics.Report
	for _, report := range reports {
		if report.Verdict != diagnostics.OK {
			unhealthy = append(unhealthy, report)
		}
	}
	terminalReports := unhealthy
	if full {
		terminalReports = reports
	}

	result := render.TriageResult{
		Namespace:     namespace,
		AllNamespaces: allNamespaces,
		Checked:       len(reports),
		Reports:       terminalReports,
		All:           reports,
		Healthy:       len(reports) - len(unhealthy),
		Full:          full,
	}

	// Every swept resource is indexed, not just the unhealthy ones printed by
	// default, so an index resolves whatever row the HTML grid's full
	// inventory shows — reports is severity-sorted, so unhealthy's own
	// positions are unaffected, a prefix of this same order.
	var entries []state.Resource
	for _, report := range reports {
		entries = append(entries, state.Resource{
			Name: report.Name, Kind: report.Kind, Namespace: report.Namespace,
		})
	}

	if len(entries) > 0 {
		// namespace is already "" for a cluster-wide sweep — blanked above, the
		// way client-go's listers spell "every namespace" — which is exactly what
		// the entry wants: no single namespace, each resource recording its own.
		// A second guard here would be dead code.
		if err := c.Save(state.State{
			Resources:     state.NewOrderedResources(entries),
			Namespace:     namespace,
			AllNamespaces: allNamespaces,
		}); err != nil {
			return render.TriageResult{}, err
		}
	}

	return result, nil
}

// sweepPage builds the HTML page for a namespace sweep from the same
// TriageResult the terminal table renders, so the two shapes cannot drift
// apart: Scope and AllNamespaces are derived from the result rather than
// re-threaded through the caller's own namespace/allNamespaces variables.
//
// Reports comes from result.All, not result.Reports: the HTML grid shows
// every swept resource unconditionally, regardless of whether --full was
// passed for the terminal table.
func sweepPage(result render.TriageResult, meta web.Meta) web.DiagPage {
	scope := result.Namespace
	if result.AllNamespaces {
		scope = render.AllNamespaces
	}
	return web.DiagPage{
		Meta:          meta,
		Scope:         scope,
		AllNamespaces: result.AllNamespaces,
		Checked:       result.Checked,
		Reports:       result.All,
	}
}

// resourcePage builds the HTML page for one indexed resource: a sweep of one,
// always Single so the template renders it inline rather than behind a
// <details>.
func resourcePage(report diagnostics.Report, meta web.Meta) web.DiagPage {
	return web.DiagPage{
		Meta:    meta,
		Scope:   report.Namespace,
		Single:  true,
		Reports: []diagnostics.Report{report},
	}
}

// eventWindow resolves how far back warning events are read: --since when it
// was given, the event_max_age setting otherwise.
//
// An empty value means the flag was absent — "" is not a duration anyone can
// type, so the flag needs no Changed() check to tell "unset" from "0", and
// --since 0 keeps its own meaning of no window at all.
func eventWindow(since string, cfg config.Config) (time.Duration, error) {
	if since == "" {
		return cfg.EventMaxAge, nil
	}
	window, err := config.ParseDuration(since)
	if err != nil {
		return 0, fmt.Errorf("'--since': %w", err)
	}
	return window, nil
}

// sinceFlag renders the resolved window for an HTML report's invocation line,
// so a saved page says which events it could have shown.
//
// Printed even when it came from the setting rather than the flag: the line
// exists to say what the page covers, and a reader cannot know the config the
// report was produced under. An unlimited window prints nothing — there is no
// window to record, and "--since 0" would read as a setting rather than as the
// absence of one.
func sinceFlag(window time.Duration) string {
	if window == 0 {
		return ""
	}
	return "--since " + config.FormatDuration(window)
}

func newDiagnosticCommand(services Services, use string, aliases []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:        use + " [index]",
		SuggestFor: []string{"triage", "health", "why", "status"},
		Short:      "Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, Ingress, Pod, or Node, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag.",
		Aliases:    aliases,
		Long: "Analyses health signals — replica counts, container states, resource usage and warning events — and reports findings by severity.\n\n" +
			"With no index, sweeps every workload in the current namespace, or in the namespace given by -n, or in every namespace with -A. Healthy resources are left out of the terminal table by default; --full includes them. The HTML report (--html) always includes them.\n\n" +
			"A Node is diagnosed by index only — from kx get nodes or kx top nodes. Nodes are not namespaced, so they do not appear in a namespace sweep or in -A.\n\n" +
			"Warning events older than 24h are ignored, so a failure from last month stops holding a verdict at warnings — and a --fail-on gate red — long after it stopped mattering. --since sets a different window (30m, 12h, 7d), --since 0 removes it, and the event_max_age setting changes the default.",
		Example: "  kx " + use + "\n  kx " + use + " 1\n  kx " + use + " -n prod\n" +
			"  kx " + use + " -A\n  kx " + use + " --html\n  kx " + use + " -A --json\n" +
			"  kx " + use + " --since 7d\n" +
			"  kx " + use + " -A --fail-on critical --out report.html",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, _ := cmd.Flags().GetString("namespace")
			allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
			full, _ := cmd.Flags().GetBool("full")
			html, _ := cmd.Flags().GetBool("html")
			port, _ := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")
			out, _ := cmd.Flags().GetString("out")
			asJSON, _ := cmd.Flags().GetBool("json")
			failOn, _ := cmd.Flags().GetString("fail-on")
			since, _ := cmd.Flags().GetString("since")
			wantsHTML := impliedHTML(html, out)
			htmlOpts := htmlOptions{Enabled: wantsHTML, Port: port, NoOpen: noOpen, Out: out}
			if err := htmlOpts.validate(
				cmd.Flags().Changed("port"), cmd.Flags().Changed("no-open")); err != nil {
				return err
			}

			if asJSON && wantsHTML {
				return fmt.Errorf(
					"'--json' cannot be combined with '%s' — one is for a "+
						"machine and the other for a browser.", htmlFlagName(html))
			}
			// Unlike kx scan's, this pair is redundant rather than impossible:
			// a document always carries every swept resource, so --full has
			// nothing to add to one. Refused rather than ignored all the same,
			// so a flag that changes nothing never looks as though it did.
			if asJSON && cmd.Flags().Changed("full") {
				return errors.New(
					"'--json' cannot be combined with '--full' — a document " +
						"already carries every resource swept, healthy ones " +
						"included, so '--full' has nothing to add to it.")
			}
			// Parsed up front so a typo fails before the cluster is read
			// rather than after a report has already been printed.
			var threshold diagnostics.Severity
			if failOn != "" {
				parsed, err := parseDiagnosticThreshold(failOn)
				if err != nil {
					return err
				}
				threshold = parsed
			}

			// Parsed here for the same reason --fail-on is: a typo should
			// cost nothing, not a sweep of every namespace first.
			window, err := eventWindow(since, services.Config)
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("namespace") && allNamespaces {
				return errors.New(
					"'--all-namespaces' and '--namespace' cannot be combined.")
			}
			// An index already carries the namespace it was listed from, so a
			// scope flag next to one is a contradiction rather than a refinement.
			scopeFlag := ""
			if cmd.Flags().Changed("namespace") {
				scopeFlag = "--namespace"
			}
			if allNamespaces {
				scopeFlag = "--all-namespaces"
			}
			if len(args) > 0 && scopeFlag != "" {
				return fmt.Errorf(
					"'%s' cannot be combined with an index — an index already "+
						"carries the namespace it was listed from. Drop the flag, "+
						"or drop the index to sweep the namespace instead.", scopeFlag)
			}
			// --full only changes what a sweep's terminal table includes; a single
			// indexed resource has nothing to include or leave out.
			if len(args) > 0 && cmd.Flags().Changed("full") {
				return errors.New(
					"'--full' cannot be combined with an index — it only affects a " +
						"namespace sweep. Drop the flag, or drop the index to sweep " +
						"the namespace instead.")
			}

			client, err := services.Kubernetes()
			if err != nil {
				return err
			}
			service := diagnostics.New(client)
			service.MaxEventAge = window
			ctx := cmd.Context()

			if len(args) == 0 {
				if namespace == "" {
					namespace = services.Kubectl.CurrentNamespace()
				}
				sweeping := "sweeping namespace"
				if allNamespaces {
					sweeping = "sweeping all namespaces"
				}
				stop := render.Status(sweeping)
				result, err := TriageCommand{
					Diagnostics: service, Save: services.State.Save,
				}.Execute(ctx, namespace, allNamespaces, full)
				stop()
				if err != nil {
					return err
				}
				if asJSON {
					document, err := triageJSON(result)
					if err != nil {
						return err
					}
					render.Raw(document)
					return sweepGate(result, failOn, threshold)
				}
				render.Triage(result)
				// The gate is the tail of every path, not the alternative to
				// one. --html says where the findings go; --fail-on says what
				// they mean, and a sweep that found something critical means
				// the same thing whether or not a browser was opened on it.
				if htmlOpts.Enabled {
					scope := namespace
					if allNamespaces {
						scope = render.AllNamespaces
					}
					meta, err := pageMeta(services.Config.Theme, "diag · "+scope,
						invocation(use, scopeArgs(namespace, allNamespaces),
							sinceFlag(window), portFlag(port)))
					if err != nil {
						return err
					}
					page, err := web.RenderDiag(sweepPage(result, meta))
					if err != nil {
						return err
					}
					// After the server stops, so Ctrl-C ends the command with
					// the exit code the sweep earned rather than servePage's nil.
					if err := deliverPage(ctx, page, htmlOpts); err != nil {
						return err
					}
				}
				return sweepGate(result, failOn, threshold)
			}

			index, err := parseIndex("index", args[0])
			if err != nil {
				return err
			}
			stop := render.Status("gathering diagnostics")
			report, err := DiagnosticCommand{
				State: services.State, Diagnostics: service,
			}.Execute(ctx, index)
			stop()
			if err != nil {
				return err
			}
			if asJSON {
				document, err := diagnosticJSON(report, index)
				if err != nil {
					return err
				}
				render.Raw(document)
				return verdictGate(report.Verdict, failOn, threshold)
			}
			render.Diagnostic(report)
			if htmlOpts.Enabled {
				meta, err := pageMeta(services.Config.Theme,
					"diag · "+string(report.Kind)+"/"+report.Name,
					invocation(use, args[0], sinceFlag(window), portFlag(port)))
				if err != nil {
					return err
				}
				page, err := web.RenderDiag(resourcePage(report, meta))
				if err != nil {
					return err
				}
				if err := deliverPage(ctx, page, htmlOpts); err != nil {
					return err
				}
			}
			return verdictGate(report.Verdict, failOn, threshold)
		},
	}
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to sweep; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"Sweep every namespace; each row is indexed and carries its own namespace")
	cmd.Flags().Bool("full", false,
		"Include healthy resources in the terminal table; the HTML report always includes them")
	cmd.Flags().Bool("json", false,
		"Print the report as JSON instead of a table")
	cmd.Flags().String("since", "",
		"Ignore warning events older than this, such as 30m, 12h or 7d; "+
			"0 for no limit (default from event_max_age, 24h)")
	cmd.Flags().String("fail-on", "",
		"Exit 2 when a verdict reaches this severity or worse (critical, warning)")
	cmd.Flags().Bool("html", false,
		"Render the report as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	cmd.Flags().String("out", "",
		"Write the HTML report to this file instead of serving it in a browser")
	return cmd
}

// verdictGate turns a verdict into an exit code when --fail-on asked for one.
//
// SilentError because the report has already been printed: this is the exit
// code, not a second error message. Two rather than one, so a pipeline can tell
// "the cluster is sick" from "kx itself failed", which is what kx exits 1 for.
func verdictGate(verdict diagnostics.Severity, failOn string, threshold diagnostics.Severity) error {
	if failOn == "" || verdict < threshold {
		return nil
	}
	return SilentError{Code: findingsExitCode}
}

// sweepGate applies the same gate to a sweep, over the worst verdict in it.
//
// Every swept resource, not just the rows the table printed: --full governs
// what fits on a screen, and a gate that changed answer with a display flag
// would be a trap.
func sweepGate(result render.TriageResult, failOn string, threshold diagnostics.Severity) error {
	if failOn == "" {
		return nil
	}
	worst := diagnostics.OK
	for _, report := range result.All {
		if report.Verdict > worst {
			worst = report.Verdict
		}
	}
	return verdictGate(worst, failOn, threshold)
}
