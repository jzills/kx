package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	if !diagnostics.SupportedKinds[kind] {
		return diagnostics.Report{}, fmt.Errorf("diagnostic is not supported for '%s'.", kind)
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

func newDiagnosticCommand(services Services, use string, aliases []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:        use + " [index]",
		SuggestFor: []string{"triage", "health", "why", "status"},
		Short:      "Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, Ingress, Pod, or Node, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag.",
		Aliases:    aliases,
		Long: "Analyses health signals — replica counts, container states, resource usage and warning events — and reports findings by severity.\n\n" +
			"With no index, sweeps every workload in the current namespace, or in the namespace given by -n, or in every namespace with -A. Healthy resources are left out of the terminal table by default; --full includes them. The HTML report (--html) always includes them.\n\n" +
			"A Node is diagnosed by index only — from kx get nodes or kx top nodes. Nodes are not namespaced, so they do not appear in a namespace sweep or in -A.",
		Example: "  kx " + use + "\n  kx " + use + " 1\n  kx " + use + " -n prod\n  kx " + use + " -A",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, _ := cmd.Flags().GetString("namespace")
			allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
			full, _ := cmd.Flags().GetBool("full")
			html, _ := cmd.Flags().GetBool("html")
			port, _ := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")
			htmlOpts := htmlOptions{Enabled: html, Port: port, NoOpen: noOpen}

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
				render.Triage(result)
				if !htmlOpts.Enabled {
					return nil
				}
				scope := namespace
				if allNamespaces {
					scope = render.AllNamespaces
				}
				meta, err := pageMeta(services.Config.Theme, "diag · "+scope,
					invocation(use, scopeArgs(namespace, allNamespaces), portFlag(port)))
				if err != nil {
					return err
				}
				page, err := web.RenderDiag(sweepPage(result, meta))
				if err != nil {
					return err
				}
				return servePage(ctx, page, htmlOpts)
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
			render.Diagnostic(report)
			if !htmlOpts.Enabled {
				return nil
			}
			meta, err := pageMeta(services.Config.Theme,
				"diag · "+string(report.Kind)+"/"+report.Name,
				invocation(use, args[0], portFlag(port)))
			if err != nil {
				return err
			}
			page, err := web.RenderDiag(resourcePage(report, meta))
			if err != nil {
				return err
			}
			return servePage(ctx, page, htmlOpts)
		},
	}
	cmd.Flags().StringP("namespace", "n", "",
		"Namespace to sweep; defaults to the current namespace")
	cmd.Flags().BoolP("all-namespaces", "A", false,
		"Sweep every namespace; each row is indexed and carries its own namespace")
	cmd.Flags().Bool("full", false,
		"Include healthy resources in the terminal table; the HTML report always includes them")
	cmd.Flags().Bool("html", false,
		"Render the report as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	return cmd
}
