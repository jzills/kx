package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
)

// sortUsage describes kx get's --sort; topSortUsage is kx top's, whose
// columns are usage rather than status.
const (
	sortUsage    = "Sort by the columns shown, before indexing: 'status,-restarts' sorts by STATUS, then by RESTARTS from most to fewest"
	topSortUsage = "Sort by the columns shown, before indexing: '-mem%,-cpu%' sorts by MEM% from most to least, then by CPU%"
)

// sortLong and topSortLong are the help paragraphs kx get and kx top carry,
// each naming its own columns and what kubectl's --sort-by takes there.
const (
	sortLong = sortHow + "Each column sorts as what " +
		"it holds — AGE as a duration, RESTARTS as a count, READY as a ratio, CPU and memory as " +
		"quantities — ties keep kubectl's order, and a `<none>` sorts last either way. kubectl's " +
		"own `--sort-by` still takes a JSONPath, and applies first."
	topSortLong = sortHow + "CPU and MEMORY sort as " +
		"quantities and CPU% and MEM% as percentages, ties keep kubectl's order, and a Pod " +
		"with no limit sorts last either way. kubectl's own `--sort-by` still takes 'cpu' or " +
		"'memory', and applies first."
	sortHow = "A `--sort` sorts the table kx draws by its own columns, before numbering it, " +
		"so row 1 is the first sorted row: name the columns separated by commas, as the header " +
		"reads them, with a leading `-` to sort one from largest down. "
)

// sortArg is how a --sort value travels with the args a listing is run with,
// and how its saved query records it: beside kubectl's flags, as kx top's
// --no-limits is, so a refresh lists it again in the same order and two
// listings sorted differently are different listings. takeSort takes it back
// out before kubectl is asked.
func sortArg(spec string) string { return "--sort=" + spec }

// takeSort reads the --sort a listing's args carry, refusing a value that
// names no column, and returns the args without it, which are kubectl's.
func takeSort(args []string) ([]index.SortKey, []string, error) {
	spec, rest, err := extractString(args, "--sort", "")
	if err != nil || spec == "" {
		return nil, rest, err
	}
	keys, err := index.ParseSortKeys(spec)
	if err != nil {
		return nil, nil, sortSpecError(spec)
	}
	return keys, rest, nil
}

// sortFlag renders a --sort for an HTML report's invocation line, so the page
// says what order its rows arrived in.
func sortFlag(spec string) string {
	if spec == "" {
		return ""
	}
	return "--sort " + spec
}

func sortSpecError(spec string) error {
	return fmt.Errorf("Invalid value for '--sort': '%s' names no column — name each column, "+
		"separated by commas: 'status,-restarts'.", spec)
}

// sortError puts a sort's refusal in kx's words: an unknown column is a value
// outside the set the listing's own header offers.
func sortError(err error) error {
	var unknown index.UnknownColumnError
	if errors.As(err, &unknown) {
		return fmt.Errorf("Invalid value for '--sort': '%s'. Accepted values: %s.",
			unknown.Column, strings.Join(unknown.Accepted, ", "))
	}
	return err
}

// unsortableError is a --sort over a reply kx could not read as a table, or
// nil when there is no sort or nothing came back: kx finds a table's rows by
// its NAME column, and a table without one was printed in kubectl's order
// as though sorted. Refused as --match is over the same reply (narrowText).
func unsortableError(output string, keys []index.SortKey, args []string) error {
	if len(keys) == 0 || strings.TrimSpace(output) == "" {
		return nil
	}
	if strings.HasPrefix(outputFormat(args), "custom-columns") {
		return errors.New("'--sort' reads a table by its NAME column, and kubectl's reply has " +
			"none — add one to the custom columns (NAME:.metadata.name), or use '--sort-by' " +
			"for kubectl's own order.")
	}
	return errors.New("'--sort' reads a table by its NAME column, and kubectl's table for " +
		"this resource has none. Use '--sort-by' for kubectl's own order.")
}

// sortRefusal is --sort beside a flag that leaves no table of kx's to sort,
// or "" for none: a watch, which streams rows as they change, and output for
// a program, which is kubectl's to order (--sort-by).
func sortRefusal(spec string, args []string) error {
	if spec == "" {
		return nil
	}
	if _, err := index.ParseSortKeys(spec); err != nil {
		return sortSpecError(spec)
	}
	if isWatch(args) {
		return errors.New("'--sort' cannot be combined with '--watch' — a watch streams rows " +
			"as they change, so there is no finished table to sort.")
	}
	if !programReadsStdout(args) {
		return nil
	}
	flag := "--no-headers"
	if output, _, _ := extractString(args, "--output", "-o"); !printsTable(args) {
		flag = "-o " + output
	}
	return fmt.Errorf("'--sort' cannot be combined with '%s' — it sorts the table kx draws, "+
		"and that output is for a program; use '--sort-by' for kubectl's own order.", flag)
}
