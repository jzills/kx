package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/graph"
	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
)

// lastRunUsage describes kx get's --last-run.
const lastRunUsage = "List only the newest Job of each CronJob, under a CRONJOB column naming it"

// lastRunLong is the help paragraph kx get carries for --last-run.
const lastRunLong = "With `jobs`, `--last-run` lists only the newest Job each CronJob has created, " +
	"under a CRONJOB column naming it, so `kx logs 2` reads that run. A Job no CronJob " +
	"created is no CronJob's last run and is left out, and a CronJob that has never run " +
	"has no row. `-l` chooses which Jobs are weighed, and the newest is picked among them."

// lastRunColumn heads the column naming each row's CronJob.
const lastRunColumn = "CRONJOB"

// lastRunRefusal is --last-run where it cannot apply, or nil: on a kind other
// than Jobs, beside an index, which already names its Job, beside a watch,
// which never finishes a listing to pick from, and beside output for a
// program, which kx does not read as rows.
func lastRunRefusal(resource string, indexed bool, args []string) error {
	if kinds.Normalize(resource) != kinds.Job {
		return fmt.Errorf("'--last-run' lists the newest Job of each CronJob, not %s — "+
			"run 'kx get jobs --last-run'.", kinds.PluralDisplay(resource))
	}
	if indexed {
		return errors.New("'--last-run' cannot be combined with an index — the index " +
			"already names its Job, so there is no newest to pick.")
	}
	if isWatch(args) {
		return errors.New("'--last-run' cannot be combined with '--watch' — a watch streams " +
			"Jobs as they change, so there is no finished listing to pick from.")
	}
	if !programReadsStdout(args) {
		return nil
	}
	flag := "--no-headers"
	if output, _, _ := extractString(args, "--output", "-o"); !printsTable(args) {
		flag = "-o " + output
	}
	return fmt.Errorf("'--last-run' cannot be combined with '%s' — it picks rows from the "+
		"table kx draws, and that output is for a program.", flag)
}

// unreadableLastRunError is --last-run over a reply kx could not read as a
// table, or nil when there is none or nothing came back: a row is matched to
// its Job by the NAME column.
func unreadableLastRunError(output string, lastRun bool) error {
	if !lastRun || strings.TrimSpace(output) == "" {
		return nil
	}
	return errors.New("'--last-run' reads a table by its NAME column, and kubectl's reply " +
		"has none — add one to the custom columns (NAME:.metadata.name).")
}

// lastRuns narrows kubectl's Job table to the newest Job of each CronJob and
// adds the CRONJOB column, returning it as a listing to narrow, sort, cut and
// number like any other. ok is false when the reply is not a table kx can
// read, which the caller refuses.
//
// Ownership is not in kubectl's table, so the same Jobs are asked for again
// as JSON — args are the table's arguments, scope its namespace, so both
// calls read the same Jobs, from the same cluster when a flag names another.
// "Newest" is graph.MostRecentJob's, the latest created: what kx diag and
// kx tree already take a CronJob's latest run to be, so the three agree.
func (c GetCommand) lastRuns(
	resource, output string, args, scope []string,
) (listing index.Listing, ok bool, err error) {
	tables, ok := index.ParseTables(output)
	if !ok {
		return index.Listing{}, false, nil
	}
	if len(tables) == 0 {
		listing, _ := c.Index.Stitch(tables)
		return listing, true, nil
	}
	query := append(getArgs(resource, args), args...)
	query = append(append(query, scope...), "-o", "json")
	reply, err := c.Kubectl.Run(query)
	if err != nil {
		return index.Listing{}, false, err
	}
	var jobs batchv1.JobList
	if err := json.Unmarshal([]byte(reply), &jobs); err != nil {
		return index.Listing{}, false, err
	}
	owners := newestRuns(jobs.Items)

	table := tables[0]
	nameIdx := index.ColumnIndex(table.Headers, "NAME")
	namespaceIdx := index.ColumnIndex(table.Headers, "NAMESPACE")
	if nameIdx < 0 {
		return index.Listing{}, false, nil
	}
	// A table of one namespace leaves its namespace unsaid; every Job the
	// reply holds was listed in it.
	namespace := ""
	if len(jobs.Items) > 0 {
		namespace = jobs.Items[0].Namespace
	}
	narrowed := index.RawTable{Headers: withColumn(table.Headers, nameIdx+1, lastRunColumn)}
	for _, row := range table.Rows {
		if nameIdx >= len(row) {
			continue
		}
		rowNamespace := namespace
		if namespaceIdx >= 0 && namespaceIdx < len(row) {
			rowNamespace = row[namespaceIdx]
		}
		owner, newest := owners[rowNamespace+"/"+row[nameIdx]]
		if !newest {
			continue
		}
		narrowed.Rows = append(narrowed.Rows, withColumn(row, nameIdx+1, owner))
	}
	listing, ok = c.Index.Stitch([]index.RawTable{narrowed})
	return listing, ok, nil
}

// newestRuns is the newest Job each CronJob owns, keyed namespace/name, with
// the name of the CronJob it ran for. A Job is a CronJob's when its
// controlling owner is one; a Job run by hand, or by another controller, is
// no CronJob's last run.
func newestRuns(jobs []batchv1.Job) map[string]string {
	cronJobs := map[types.UID]string{}
	for _, job := range jobs {
		for _, ref := range job.OwnerReferences {
			if ref.Kind == string(kinds.CronJob) && ref.Controller != nil && *ref.Controller {
				cronJobs[ref.UID] = ref.Name
			}
		}
	}
	newest := make(map[string]string, len(cronJobs))
	for uid, name := range cronJobs {
		if job := graph.MostRecentJob(uid, jobs); job != nil {
			newest[job.Namespace+"/"+job.Name] = name
		}
	}
	return newest
}

// withColumn is row with value inserted at position, leaving row itself
// untouched.
func withColumn(row []string, position int, value string) []string {
	widened := make([]string, 0, len(row)+1)
	widened = append(widened, row[:position]...)
	widened = append(widened, value)
	return append(widened, row[position:]...)
}
