package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
)

// limitUsage describes --limit on the listing commands that take it.
const limitUsage = "Keep the first N rows, after --match and --sort and before indexing"

// limitLong is the help paragraph kx get, kx secret and kx top carry.
const limitLong = "A `--limit` keeps the first N rows, after `--match` narrows them and " +
	"`--sort` orders them and before they are numbered, so `--sort age --limit 5` is " +
	"the five newest numbered 1 to 5, and an index past them names nothing. The caption " +
	"counts the rows kept against the rows there were."

// limitFlag renders a --limit for an HTML report's invocation line.
func limitFlag(limit int) string {
	if limit == 0 {
		return ""
	}
	return fmt.Sprintf("--limit %d", limit)
}

// limitArg is a --limit as a relist names it.
func limitArg(limit int) string { return fmt.Sprintf("--limit=%d", limit) }

// limitOf reads a command's --limit, refusing a count that keeps no rows.
// Zero, unset, is no limit; typed, it is refused rather than read that way,
// as `--sort ""` is.
func limitOf(changed bool, limit int) (int, error) {
	if changed && limit < 1 {
		return 0, fmt.Errorf("Invalid value for '--limit': '%d' keeps no rows — "+
			"give a count of 1 or more.", limit)
	}
	return limit, nil
}

// limited cuts a sorted listing to its first limit rows, returning the rows
// it had before the cut when it made one and zero when it did not, which is
// what index.Table.Of records.
func limited(listing index.Listing, limit int) (index.Listing, int) {
	cut := listing.Limit(limit)
	if cut.Len() == listing.Len() {
		return listing, 0
	}
	return cut, listing.Len()
}

// limitedRows is limited for one table already parsed into rows.
func limitedRows(rows [][]string, limit int) ([][]string, int) {
	cut := index.LimitRows(rows, limit)
	if len(cut) == len(rows) {
		return rows, 0
	}
	return cut, len(rows)
}

// limitRefusal is --limit beside a flag that leaves no table of kx's to cut,
// or nil for none: a watch, which never finishes a table, and output for a
// program, which `head` or `jq` cuts as well as kx could.
func limitRefusal(limit int, args []string) error {
	if limit == 0 {
		return nil
	}
	if isWatch(args) {
		return errors.New("'--limit' cannot be combined with '--watch' — a watch streams rows " +
			"as they change, so there is no finished table to cut.")
	}
	if !programReadsStdout(args) {
		return nil
	}
	flag := "--no-headers"
	if output, _, _ := extractString(args, "--output", "-o"); !printsTable(args) {
		flag = "-o " + output
	}
	return fmt.Errorf("'--limit' cannot be combined with '%s' — it cuts the table kx draws, "+
		"and that output is for a program; pipe it through 'head' instead.", flag)
}

// unlimitableError is a --limit over a reply kx could not read as a table, or
// nil when there is no limit or nothing came back. kx finds a table's rows by
// its NAME column, so a table without one cannot be cut by row, and printing
// it whole answered a limit nothing had applied.
func unlimitableError(output string, limit int) error {
	if limit == 0 || strings.TrimSpace(output) == "" {
		return nil
	}
	return errors.New("'--limit' reads a table by its NAME column, and kubectl's reply has " +
		"none — pipe it through 'head' instead.")
}
