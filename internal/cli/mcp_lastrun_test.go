package cli

import (
	"slices"
	"strings"
	"testing"
)

type lastRunListOutput struct {
	LastRun   bool `json:"lastRun"`
	Total     int  `json:"total"`
	Resources []struct {
		Index   int    `json:"index"`
		Name    string `json:"name"`
		CronJob string `json:"cronJob"`
	} `json:"resources"`
}

// list_resources with lastRun narrows a Job listing as kx get jobs --last-run
// does, asking kubectl for the same Jobs as JSON in the same namespace, and
// names each row's CronJob. The control is the same reply without lastRun,
// which lists every Job from one call and names no CronJob.
func TestListResourcesLastRunListsTheNewestJobOfEachCronJob(t *testing.T) {
	kube := &recordingKubectl{outputs: []string{lastRunJobs, lastRunJSON}, namespace: "prod"}
	var out lastRunListOutput
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "list_resources",
		map[string]any{"kind": "jobs", "lastRun": true}), &out)
	var got []string
	for _, row := range out.Resources {
		got = append(got, row.Name+"/"+row.CronJob)
	}
	if want := []string{"backup-200/backup", "report-100/report"}; !slices.Equal(got, want) {
		t.Errorf("resources = %v, want %v", got, want)
	}
	if !out.LastRun || out.Total != 2 {
		t.Errorf("lastRun = %v, total = %d, want true and 2", out.LastRun, out.Total)
	}
	if len(kube.runs) != 2 || joinArgs(kube.runs[1]) != "get jobs -n prod -o json" {
		t.Errorf("kubectl = %q, want the listing and then get jobs -n prod -o json", kube.runs)
	}

	kube = &recordingKubectl{outputs: []string{lastRunJobs, lastRunJSON}, namespace: "prod"}
	out = lastRunListOutput{}
	decodeStructured(t, callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "list_resources",
		map[string]any{"kind": "jobs"}), &out)
	if len(out.Resources) != 5 || out.Resources[0].CronJob != "" || len(kube.runs) != 1 {
		t.Errorf("control: resources = %+v from %q, want all five Jobs, no CronJob, one call", out.Resources, kube.runs)
	}
}

// With --write-listings, the listing is saved as kx get jobs --last-run saves
// it, LastRun and all, so the user's refresh of it lists the newest runs
// again; and each row's index resolves to that run.
func TestWriteListingsListResourcesLastRunSavesLikeKxGet(t *testing.T) {
	deps := writingDeps(t, &recordingKubectl{outputs: []string{lastRunJobs, lastRunJSON}, namespace: "prod"})
	var out lastRunListOutput
	decodeStructured(t, callTool(t, connectMCP(t, deps), "list_resources",
		map[string]any{"kind": "jobs", "lastRun": true}), &out)

	entry := onlyTaggedEntry(t, deps.State)
	cli := cliState(t)
	if _, _, err := (GetCommand{
		Kubectl: &recordingKubectl{outputs: []string{lastRunJobs, lastRunJSON}, namespace: "prod"},
		State:   cli, Index: indexService(), LastRun: true,
	}).Execute("jobs", "", nil); err != nil {
		t.Fatal(err)
	}
	assertSavedLikeTheCLI(t, entry, cli)
	if !entry.Query.LastRun {
		t.Errorf("saved query = %+v, want LastRun", entry.Query)
	}
	for i, row := range out.Resources {
		if row.Index != i+1 {
			t.Errorf("row %d carries index %d", i, row.Index)
		}
		assertResolves(t, deps.State, row.Index, "Job", row.Name, "prod")
	}
}

// lastRun names one kind, and is refused on any other before kubectl runs.
func TestListResourcesLastRunRefusesOtherKinds(t *testing.T) {
	kube := &recordingKubectl{namespace: "prod"}
	result := callTool(t, connectMCP(t, mcpTestDeps(t, kube)), "list_resources",
		map[string]any{"kind": "cronjobs", "lastRun": true})
	want := "'lastRun' lists the newest Job of each CronJob, not CronJobs — set kind to jobs."
	if !result.IsError || !strings.Contains(toolText(result), want) {
		t.Errorf("result = %q, want an error containing %q", toolText(result), want)
	}
	if len(kube.runs) > 0 {
		t.Errorf("kubectl was called: %q", kube.runs)
	}
}
