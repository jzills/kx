package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jzills/kx/internal/state"
)

// kubectl's Job table for prod: two runs of backup, one of report, a Job run
// by hand, and one an operator's custom resource created.
const lastRunJobs = "NAME         STATUS     COMPLETIONS   DURATION   AGE\n" +
	"backup-100   Complete   1/1           40s        30m\n" +
	"backup-200   Complete   1/1           42s        12m\n" +
	"report-100   Failed     0/1           3m         2h\n" +
	"manual       Complete   1/1           5s         1d\n" +
	"operated     Complete   1/1           5s         1m\n"

// ownedJob is one Job of a kubectl get jobs -o json reply, controlled by the
// CronJob owner (with uid) when owner is not empty — or, spelled Kind/name, by
// an owner of that kind.
func ownedJob(namespace, name, created, owner, uid string) string {
	refs := "[]"
	if owner != "" {
		kind, ownerName, other := strings.Cut(owner, "/")
		if !other {
			kind, ownerName = "CronJob", owner
		}
		refs = fmt.Sprintf(`[{"apiVersion":"batch/v1","kind":%q,"name":%q,"uid":%q,"controller":true}]`,
			kind, ownerName, uid)
	}
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q,"creationTimestamp":%q,"ownerReferences":%s}}`,
		name, namespace, created, refs)
}

func jobList(items ...string) string {
	return `{"apiVersion":"v1","kind":"List","items":[` + strings.Join(items, ",") + `]}`
}

var lastRunJSON = jobList(
	ownedJob("prod", "backup-100", "2026-10-26T10:00:00Z", "backup", "u-backup"),
	ownedJob("prod", "backup-200", "2026-10-26T10:15:00Z", "backup", "u-backup"),
	ownedJob("prod", "report-100", "2026-10-26T08:00:00Z", "report", "u-report"),
	ownedJob("prod", "manual", "2026-10-25T10:00:00Z", "", ""),
	ownedJob("prod", "operated", "2026-10-26T11:00:00Z", "Workflow/nightly", "u-workflow"),
)

// --last-run keeps the newest Job of each CronJob and names the CronJob in a
// column of its own, so the indexes resolve to those runs alone: an older
// run of the same CronJob, a Job run by hand and one another controller
// created are left out. Ownership is
// read from a second call, for the same Jobs as JSON. The control is the same
// reply without the flag, which numbers every Job and asks kubectl once.
func TestGetLastRunListsTheNewestJobOfEachCronJob(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{lastRunJobs, lastRunJSON}}
	services := switchServices(t, kube)
	out := captureRender(t)
	if err := executeArgv(newGetCommand(services), []string{"jobs", "--last-run"}); err != nil {
		t.Fatalf("kx get jobs --last-run: %v", err)
	}
	if got, want := names(t, services.State), []string{"backup-200", "report-100"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if len(kube.calls) != 2 || joinArgs(kube.calls[1]) != "get jobs -o json" {
		t.Errorf("kubectl = %q, want the table and then get jobs -o json", kube.calls)
	}
	for _, want := range []string{"Jobs · prod · 2 items", "CRONJOB", "backup-200    backup", "report-100    report"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	entry, _ := services.State.Load()
	if !entry.Query.LastRun || slices.Contains(entry.Query.Args, "--last-run") {
		t.Errorf("saved query = %+v, want LastRun, apart from kubectl's args", entry.Query)
	}

	kube = &scriptedKubectl{outputs: []string{lastRunJobs, lastRunJSON}}
	services = switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services), []string{"jobs"}); err != nil {
		t.Fatalf("kx get jobs: %v", err)
	}
	if got := names(t, services.State); len(got) != 5 {
		t.Errorf("control: indexes resolve to %v, want all five Jobs", got)
	}
	if len(kube.calls) != 1 {
		t.Errorf("control: kubectl = %q, want one call", kube.calls)
	}
}

// Across namespaces, two CronJobs sharing a name are two CronJobs, each with
// its own newest run, and each row keeps its own namespace.
func TestGetLastRunAcrossNamespacesKeepsSameNamedCronJobsApart(t *testing.T) {
	table := "NAMESPACE   NAME         STATUS     COMPLETIONS   DURATION   AGE\n" +
		"prod        backup-100   Complete   1/1           40s        30m\n" +
		"prod        backup-200   Complete   1/1           42s        12m\n" +
		"staging     backup-100   Complete   1/1           40s        3h\n" +
		"staging     backup-300   Failed     0/1           1m         1h\n"
	reply := jobList(
		ownedJob("prod", "backup-100", "2026-10-26T10:00:00Z", "backup", "u-prod"),
		ownedJob("prod", "backup-200", "2026-10-26T10:15:00Z", "backup", "u-prod"),
		ownedJob("staging", "backup-100", "2026-10-26T07:00:00Z", "backup", "u-staging"),
		ownedJob("staging", "backup-300", "2026-10-26T09:00:00Z", "backup", "u-staging"),
	)
	kube := &scriptedKubectl{outputs: []string{table, reply}}
	services := switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services), []string{"jobs", "-A", "--last-run"}); err != nil {
		t.Fatalf("kx get jobs -A --last-run: %v", err)
	}
	var got []string
	for i := 1; i <= 2; i++ {
		name, namespace, _, err := services.State.Fields(i)
		if err != nil {
			t.Fatalf("Fields(%d): %v", i, err)
		}
		got = append(got, namespace+"/"+name)
	}
	if want := []string{"prod/backup-200", "staging/backup-300"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
	if got := joinArgs(kube.calls[1]); got != "get jobs --all-namespaces -o json" {
		t.Errorf("ownership call = %q, want it across namespaces too", got)
	}
}

// The CRONJOB column is a column like any other: sorted by and cut after.
func TestGetLastRunSortsAndCutsByItsColumn(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{lastRunJobs, lastRunJSON}}
	services := switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services), []string{"jobs", "--last-run", "--sort", "-cronjob", "--limit", "1"}); err != nil {
		t.Fatalf("kx get jobs --last-run --sort -cronjob --limit 1: %v", err)
	}
	if got, want := names(t, services.State), []string{"report-100"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}

// A saved last-run listing is listed again as one by the replay a refresh
// runs, and the command to relist it names the flag: relisted without it,
// row 2 is another Job.
func TestGetLastRunIsReplayedFromTheSavedQuery(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{lastRunJobs, lastRunJSON}}
	services := switchServices(t, kube)
	quietRender(t)
	entry := state.State{Namespace: "prod", Query: &state.Query{Resource: "jobs", Args: []string{}, LastRun: true}}
	if _, err := replayGet(services, entry, false); err != nil {
		t.Fatalf("replayGet: %v", err)
	}
	if got, want := names(t, services.State), []string{"backup-200", "report-100"}; !slices.Equal(got, want) {
		t.Errorf("replayed listing resolves to %v, want %v", got, want)
	}
	if got, want := relistCommand(entry, false), "kx get jobs -n prod --last-run"; got != want {
		t.Errorf("relist = %q, want %q", got, want)
	}
}

// Another cluster's Jobs are read from that cluster both times, and printed
// unnumbered as any other cluster's listing is.
func TestGetLastRunReadsAnotherClusterTwice(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{lastRunJobs, lastRunJSON}}
	services := switchServices(t, kube)
	stdout, _, err := runCaptured(t, newGetCommand(services), []string{"jobs", "--context=b", "--last-run"})
	if err != nil {
		t.Fatalf("kx get jobs --context=b --last-run: %v", err)
	}
	if got := joinArgs(kube.calls[1]); got != "get jobs --context=b -o json" {
		t.Errorf("ownership call = %q, want it in cluster b", got)
	}
	if !strings.Contains(stdout, "backup-200") || strings.Contains(stdout, "backup-100") || strings.Contains(stdout, "manual") {
		t.Errorf("want the newest runs alone:\n%s", stdout)
	}
}

// Each refusal is made before kubectl is asked.
func TestGetLastRunRefusals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cronjobs", "--last-run"},
			"'--last-run' lists the newest Job of each CronJob, not CronJobs — run 'kx get jobs --last-run'."},
		{[]string{"pods", "--last-run"},
			"'--last-run' lists the newest Job of each CronJob, not Pods — run 'kx get jobs --last-run'."},
		{[]string{"jobs", "1", "--last-run"},
			"'--last-run' cannot be combined with an index — the index already names its Job, so there is no newest to pick."},
		{[]string{"jobs", "--last-run", "--watch"},
			"'--last-run' cannot be combined with '--watch' — a watch streams Jobs as they change, so there is no finished listing to pick from."},
		{[]string{"jobs", "--last-run", "-o", "yaml"},
			"'--last-run' cannot be combined with '-o yaml' — it picks rows from the table kx draws, and that output is for a program."},
		{[]string{"jobs", "--last-run", "--no-headers"},
			"'--last-run' cannot be combined with '--no-headers' — it picks rows from the table kx draws, and that output is for a program."},
	} {
		kube := &scriptedKubectl{}
		services := switchServices(t, kube)
		if err := services.State.Save(state.State{Namespace: "prod", Resources: state.NewOrderedResources(
			[]state.Resource{{Name: "backup-200", Kind: "Job"}}), Query: &state.Query{Resource: "jobs"}}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		err := executeArgv(newGetCommand(services), tc.args)
		if err == nil || err.Error() != tc.want {
			t.Errorf("kx get %s: err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
		}
		if len(kube.calls) > 0 {
			t.Errorf("kx get %s: kubectl was called: %q", strings.Join(tc.args, " "), kube.calls)
		}
	}
}

// A table with no NAME column cannot be matched to its Jobs. The control is
// the same columns with a NAME among them, which are.
func TestGetLastRunRefusesATableWithNoNameColumn(t *testing.T) {
	kube := &scriptedKubectl{outputs: []string{"JOB          STATUS\nbackup-200   Complete\n", lastRunJSON}}
	services := switchServices(t, kube)
	quietRender(t)
	err := executeArgv(newGetCommand(services),
		[]string{"jobs", "--last-run", "-o", "custom-columns=JOB:.metadata.name,STATUS:.status.conditions[0].type"})
	if want := "'--last-run' reads a table by its NAME column, and kubectl's reply has none — " +
		"add one to the custom columns (NAME:.metadata.name)."; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}

	kube = &scriptedKubectl{outputs: []string{"NAME         STATUS\nbackup-100   Complete\nbackup-200   Complete\n", lastRunJSON}}
	services = switchServices(t, kube)
	quietRender(t)
	if err := executeArgv(newGetCommand(services),
		[]string{"jobs", "--last-run", "-o", "custom-columns=NAME:.metadata.name,STATUS:.status.conditions[0].type"}); err != nil {
		t.Fatalf("NAME column: %v", err)
	}
	if got, want := names(t, services.State), []string{"backup-200"}; !slices.Equal(got, want) {
		t.Errorf("indexes resolve to %v, want %v", got, want)
	}
}
