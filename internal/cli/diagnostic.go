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
//
// A cluster-wide sweep saves no state: there are no indexes for a
// cross-namespace listing to protect, since names repeat across namespaces.
// kx get -A follows the same rule.
func (c TriageCommand) Execute(
	ctx context.Context, namespace string, allNamespaces bool,
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

	var unhealthy []diagnostics.Report
	for _, report := range reports {
		if report.Verdict != diagnostics.OK {
			unhealthy = append(unhealthy, report)
		}
	}
	// Most severe first, stable so the sweep's order survives within a severity.
	sort.SliceStable(unhealthy, func(i, j int) bool {
		return unhealthy[i].Verdict > unhealthy[j].Verdict
	})

	// A cluster-wide sweep neither saves state nor drops rows: there are no
	// indexes to build entries for, and none to protect from a repeated name.
	if allNamespaces {
		return render.TriageResult{
			Namespace:     namespace,
			AllNamespaces: true,
			Checked:       len(reports),
			Reports:       unhealthy,
			Healthy:       len(reports) - len(unhealthy),
		}, nil
	}

	var entries []state.Resource
	for _, report := range unhealthy {
		entries = append(entries, state.Resource{Name: report.Name, Kind: report.Kind})
	}

	if len(unhealthy) > 0 {
		if err := c.Save(state.State{
			Resources: state.NewOrderedResources(entries),
			Namespace: namespace,
		}); err != nil {
			return render.TriageResult{}, err
		}
	}

	return render.TriageResult{
		Namespace: namespace,
		Checked:   len(reports),
		Reports:   unhealthy,
		Healthy:   len(reports) - len(unhealthy),
	}, nil
}

// sweepPage builds the HTML page for a namespace sweep from the same
// TriageResult the terminal table renders, so the two shapes cannot drift
// apart: Scope and AllNamespaces are derived from the result rather than
// re-threaded through the caller's own namespace/allNamespaces variables.
func sweepPage(result render.TriageResult, meta web.Meta) web.DiagPage {
	scope := result.Namespace
	if result.AllNamespaces {
		scope = "all namespaces"
	}
	return web.DiagPage{
		Meta:          meta,
		Scope:         scope,
		AllNamespaces: result.AllNamespaces,
		Checked:       result.Checked,
		Healthy:       result.Healthy,
		Reports:       result.Reports,
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
		Use:     use + " [index]",
		Short:   "Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, or Pod, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag.",
		Aliases: aliases,
		Long: "Analyses health signals — replica counts, container states, resource\n" +
			"usage and warning events — and reports findings by severity.\n" +
			"With no index, sweeps every workload in the current namespace, or in\n" +
			"the namespace given by -n, or in every namespace with -A.",
		Example: "  kx " + use + "\n  kx " + use + " 1\n  kx " + use + " -n prod\n  kx " + use + " -A",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, _ := cmd.Flags().GetString("namespace")
			allNamespaces, _ := cmd.Flags().GetBool("all-namespaces")
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
				}.Execute(ctx, namespace, allNamespaces)
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
					scope = "all namespaces"
				}
				meta, err := pageMeta(services.Config.Theme, "kx diag · "+scope,
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
				"kx diag · "+string(report.Kind)+"/"+report.Name,
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
		"Sweep every namespace; results are not indexed")
	cmd.Flags().Bool("html", false,
		"Render the report as HTML and serve it in a browser")
	cmd.Flags().Int("port", 0,
		"Port to serve the HTML report on; 0 picks a free one")
	cmd.Flags().Bool("no-open", false,
		"Serve the HTML report without opening a browser")
	return cmd
}
