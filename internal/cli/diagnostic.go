package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/jzills/kx/internal/diagnostics"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/render"
	"github.com/jzills/kx/internal/state"
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

func (c TriageCommand) Execute(ctx context.Context, namespace string) (render.TriageResult, error) {
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

	// State is keyed by name alone, so a rare cross-kind name collision keeps
	// the earlier (more severe) row and drops the later one rather than letting
	// an index resolve to the wrong resource.
	seen := map[string]bool{}
	var entries []state.Resource
	var displayed []diagnostics.Report
	var dropped []string
	for _, report := range unhealthy {
		if seen[report.Name] {
			dropped = append(dropped, string(report.Kind)+"/"+report.Name)
			continue
		}
		seen[report.Name] = true
		entries = append(entries, state.Resource{Name: report.Name, Kind: report.Kind})
		displayed = append(displayed, report)
	}

	if len(displayed) > 0 {
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
		Reports:   displayed,
		Healthy:   len(reports) - len(unhealthy),
		Dropped:   dropped,
	}, nil
}

func newDiagnosticCommand(services Services, use string, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     use + " [index]",
		Short:   "Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, or Pod, or triage the whole namespace when no index is given; alias: kx diag.",
		Aliases: aliases,
		Long: "Analyses health signals — replica counts, container states, resource\n" +
			"usage and warning events — and reports findings by severity.\n" +
			"With no index, sweeps every workload in the current namespace.",
		Example: "  kx " + use + "\n  kx " + use + " 1",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := services.Kubernetes()
			if err != nil {
				return err
			}
			service := diagnostics.New(client)
			ctx := cmd.Context()

			if len(args) == 0 {
				namespace := services.Kubectl.CurrentNamespace()
				stop := render.Status("sweeping namespace")
				result, err := TriageCommand{
					Diagnostics: service, Save: services.State.Save,
				}.Execute(ctx, namespace)
				stop()
				if err != nil {
					return err
				}
				render.Triage(result)
				return nil
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
			return nil
		},
	}
}
