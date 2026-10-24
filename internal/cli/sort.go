package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jzills/kx/internal/index"
)

// sortUsage describes kx get's --sort; topSortUsage is kx top's, whose
// columns are usage rather than status, and secretSortUsage kx secret's,
// whose listing is the four columns kubectl prints for a Secret.
const (
	sortUsage       = "Sort by the columns shown, before indexing: 'status,-restarts' sorts by STATUS, then by RESTARTS from most to fewest"
	topSortUsage    = "Sort by the columns shown, before indexing: '-mem%,-cpu%' sorts by MEM% from most to least, then by CPU%"
	secretSortUsage = "Sort by the columns shown, before indexing: 'type,-age' sorts by TYPE, then by AGE from oldest to newest"
)

// sortLong, topSortLong and secretSortLong are the help paragraphs kx get, kx
// top and kx secret carry, each naming its own columns and what kubectl's
// --sort-by takes there.
const (
	sortLong = sortHow + "Each column sorts as what " +
		"it holds — AGE as a duration, RESTARTS as a count, READY as a ratio, CPU and memory as " +
		"quantities — ties keep kubectl's order, and a `<none>` sorts last either way. kubectl's " +
		"own `--sort-by` still takes a JSONPath, and applies first."
	topSortLong = sortHow + "CPU and MEMORY sort as " +
		"quantities and CPU% and MEM% as percentages, ties keep kubectl's order, and a Pod " +
		"with no limit sorts last either way. Another cluster's listing — `--context`, " +
		"`--kubeconfig` — prints kubectl's own table, which kx never adds its percentage " +
		"columns to: name 'cpu' or 'memory' there for Pods, and kubectl's own 'cpu%' or " +
		"'memory%' for Nodes. kubectl's own `--sort-by` still takes 'cpu' or " +
		"'memory', and applies first."
	secretSortLong = sortHow + "A Secret listing's columns are NAME, " +
		"TYPE, DATA and AGE, which sort as what they hold — DATA as a count, AGE as a " +
		"duration — and ties keep kubectl's order. With `--decode` there is no listing to " +
		"sort, only one Secret's data, so the two cannot be combined. kubectl's own " +
		"`--sort-by` still takes a JSONPath, and applies first."
	sortHow = "A `--sort` sorts the table kx draws by its own columns, before numbering it, " +
		"so row 1 is the first sorted row: name the columns separated by commas, as the header " +
		"reads them, with a leading `-` to sort one from largest down. "
)

// sortArg is a --sort as a relist names it, beside the args its query
// repeats.
func sortArg(spec string) string { return "--sort=" + spec }

// sortKeysOf reads a listing's --sort, refusing a value that names no column.
// The spec travels apart from kubectl's args, as the query records it
// (state.Query.Sort): among them, a --sort typed after `--` for kubectl was
// read as kx's.
func sortKeysOf(spec string) ([]index.SortKey, error) {
	if spec == "" {
		return nil, nil
	}
	keys, err := index.ParseSortKeys(spec)
	if err != nil {
		return nil, sortSpecError(spec)
	}
	return keys, nil
}

// sortFlag renders a --sort for an HTML report's invocation line, so the page
// says what order its rows arrived in. Quoted as a relist hint's is
// (shellWord): a column spelled with a space is one word of the flag, and
// `--sort nominated node` pasted back looked up a Pod named node.
func sortFlag(spec string) string {
	if spec == "" {
		return ""
	}
	return "--sort " + shellWord(spec)
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

// sumSortRefusal is kx top's --sort beside kubectl's --sum, which ends the
// table with a total row of underscores. kx reads that row as a Pod's, and a
// column has one kind of value throughout or none (index.columnValues), so
// the underscores turned MEMORY(bytes) to text: 9Mi sorted above 100Mi, and
// the total row itself landed wherever its text fell, numbered 1.
func sumSortRefusal(spec string, args []string) error {
	if spec == "" {
		return nil
	}
	if sum, _ := extractBool(args, "--sum"); !sum {
		return nil
	}
	return errors.New("'--sort' cannot be combined with '--sum' — kubectl's total row is " +
		"no Pod to sort among the rest. Use '--sort-by' for kubectl's own order.")
}
