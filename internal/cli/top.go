package cli

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
	"github.com/jzills/kx/internal/web"
)

// TopCommand lists pod resource usage, indexed like `kx get`, with usage
// percentages against each pod's limits.
type TopCommand struct {
	Kubectl kubectl.Service
	State   StateWriter
	Index   Indexer
}

// EnsureAvailable checks that the cluster's metrics API is registered
// before kx top tries to use it — kubectl top depends on the metrics-server
// add-on, not just kubectl itself, and without this check a missing
// metrics-server surfaces as kubectl's own raw, unhelpful error text.
// Mirrors ScanCommand.EnsureAvailable's preflight-probe shape exactly
// (internal/cli/scan.go): a cheap probe, a friendly message on failure.
func (c TopCommand) EnsureAvailable() error {
	if c.Kubectl.Probe([]string{"get", "--raw", "/apis/metrics.k8s.io/v1beta1"}) != 0 {
		return errors.New("metrics-server is not available — kx top needs it " +
			"installed in the cluster. Install it: " +
			"https://github.com/kubernetes-sigs/metrics-server#installation")
	}
	return nil
}

// Execute returns the indexed table to display, and the namespace it was
// listed from — resolving that costs a `kubectl config view` when no -n was
// given, and the caller needs the same answer for the caption.
func (c TopCommand) Execute(
	filterTerm string, extraArgs []string, noLimits bool,
) (table index.Table, namespace string, err error) {
	if err := c.EnsureAvailable(); err != nil {
		return index.Table{}, "", err
	}
	output, err := c.Kubectl.Run(append([]string{"top", "pods"}, extraArgs...))
	if err != nil {
		return index.Table{}, "", err
	}
	allNamespaces := allNamespaces(extraArgs)
	// --containers is a different table shape entirely, so it never gets
	// percentage columns — unlike -A, which does (see withUsagePercentages).
	hasContainers := false
	for _, arg := range extraArgs {
		if arg == "--containers" {
			hasContainers = true
			break
		}
	}

	namespace = extractNamespace(extraArgs)
	if namespace == "" {
		namespace = c.Kubectl.CurrentNamespace()
	}
	// Parsed once here: the rows are narrowed by --match and widened with the
	// percentage columns before anything numbers them, and none of that goes
	// back through text on the way.
	headers, rows, _ := index.ParseTable(output)
	if headers == nil {
		return c.Index.Add(output), namespace, nil
	}
	if filterTerm != "" {
		rows = index.FilterRows(headers, rows, filterTerm)
	}
	if !hasContainers && !noLimits {
		headers, rows, err = c.withUsagePercentages(headers, rows, namespace, allNamespaces)
		if err != nil {
			return index.Table{}, "", err
		}
	}

	indexed := c.Index.AddRows(headers, rows)
	if len(indexed.Entries) > 0 {
		var match *string
		if filterTerm != "" {
			match = &filterTerm
		}
		if extraArgs == nil {
			extraArgs = []string{}
		}
		// An -A listing has no single namespace for the entry — each resource
		// carries its own, read from the table's NAMESPACE column, exactly as
		// `kx get -A` records them.
		entryNamespace := namespace
		if allNamespaces {
			entryNamespace = ""
		}
		if err := c.State.Save(state.State{
			Resources: resourcesFrom(indexed.Entries, kinds.Pod),
			Namespace: entryNamespace,
			// Recorded as a `get pods` query so a stale entry refreshes into a
			// listing, which is what the indexes were assigned against.
			Query: &state.Query{Resource: "pods", Args: extraArgs, Match: match},
		}); err != nil {
			return index.Table{}, "", err
		}
	}
	return indexed, namespace, nil
}

// ExecuteNodes lists node CPU/memory usage, indexed like kx get nodes.
//
// Unlike Execute (pods), there is no limits lookup: kubectl top nodes
// already reports CPU(%)/MEMORY(%) against node capacity natively, so this
// only has to relabel those columns to kx's own CPU%/MEM% naming (values
// untouched) — see relabelPercentColumns — so the existing IndexedTable
// coloring, which keys off those exact header names, applies for free.
func (c TopCommand) ExecuteNodes(
	filterTerm string, extraArgs []string,
) (table index.Table, namespace string, err error) {
	if err := c.EnsureAvailable(); err != nil {
		return index.Table{}, "", err
	}
	output, err := c.Kubectl.Run(append([]string{"top", "nodes"}, extraArgs...))
	if err != nil {
		return index.Table{}, "", err
	}
	namespace = extractNamespace(extraArgs)
	if namespace == "" {
		namespace = c.Kubectl.CurrentNamespace()
	}

	headers, rows, _ := index.ParseTable(output)
	if headers == nil {
		return c.Index.Add(output), namespace, nil
	}
	if filterTerm != "" {
		rows = index.FilterRows(headers, rows, filterTerm)
	}
	headers = relabelPercentColumns(headers)

	indexed := c.Index.AddRows(headers, rows)
	if len(indexed.Entries) > 0 {
		if extraArgs == nil {
			extraArgs = []string{}
		}
		var match *string
		if filterTerm != "" {
			match = &filterTerm
		}
		if err := c.State.Save(state.State{
			Resources: resourcesFrom(indexed.Entries, kinds.Node),
			Namespace: namespace,
			// Recorded as a `get nodes` query, matching kx get nodes' own
			// convention, so a stale entry refreshes into the same listing
			// shape the indexes were assigned against.
			Query: &state.Query{Resource: "nodes", Args: extraArgs, Match: match},
		}); err != nil {
			return index.Table{}, "", err
		}
	}
	return indexed, namespace, nil
}

// relabelPercentColumns renames kubectl top nodes' native CPU(%)/MEMORY(%)
// columns to kx's own CPU%/MEM% naming. Values are untouched — this is a
// header rewrite only, so the existing render.UsageStyle-driven coloring in
// IndexedTable (which looks up cells by the exact header names "CPU%"/
// "MEM%") applies to nodes without any new coloring code.
func relabelPercentColumns(headers []string) []string {
	cpuCol := indexOfHeader(headers, "CPU(%)")
	memCol := indexOfHeader(headers, "MEMORY(%)")
	if cpuCol < 0 && memCol < 0 {
		return headers
	}
	relabeled := append([]string{}, headers...)
	if cpuCol >= 0 {
		relabeled[cpuCol] = "CPU%"
	}
	if memCol >= 0 {
		relabeled[memCol] = "MEM%"
	}
	return relabeled
}

// withUsagePercentages appends CPU%/MEM% columns computed against each
// pod's limits. When allNamespaces, rows are keyed by their own NAMESPACE
// column (present in kubectl top pods -A's own output) rather than the
// single namespace passed in — pod names collide across namespaces, so a
// bare-name key would silently give two different pods' rows the same
// limit.
func (c TopCommand) withUsagePercentages(
	headers []string, rows [][]string, namespace string, allNamespaces bool,
) ([]string, [][]string, error) {
	nameIdx := indexOfHeader(headers, "NAME")
	cpuCol := indexOfHeader(headers, "CPU(cores)")
	memCol := indexOfHeader(headers, "MEMORY(bytes)")
	if nameIdx < 0 || cpuCol < 0 || memCol < 0 {
		return headers, rows, nil
	}
	namespaceIdx := indexOfHeader(headers, "NAMESPACE")

	limits, err := c.podLimits(namespace, allNamespaces)
	if err != nil {
		return nil, nil, err
	}

	widened := make([][]string, 0, len(rows))
	for _, row := range rows {
		rowNamespace := namespace
		if namespaceIdx >= 0 {
			rowNamespace = row[namespaceIdx]
		}
		limit := limits[rowNamespace+"/"+row[nameIdx]]
		widened = append(widened, append(append([]string{}, row...),
			percentCell(row[cpuCol], limit.CPU),
			percentCell(row[memCol], limit.Memory),
		))
	}
	return append(append([]string{}, headers...), "CPU%", "MEM%"), widened, nil
}

// podLimit is a pod's summed limits. A nil quantity means the limit is
// undefined for that resource.
type podLimit struct {
	CPU    *resource.Quantity
	Memory *resource.Quantity
}

// podLimits sums each resource's limit across a pod's containers, matching
// how `kubectl top pods` aggregates usage to the pod level, for one
// namespace or (allNamespaces) the whole cluster. Keyed by
// "namespace/name" in both cases — pod names alone collide across
// namespaces, so a single shared key shape (rather than a bare-name map
// for the single-namespace case and a different one for -A) is what lets
// withUsagePercentages use one lookup expression regardless of scope.
//
// A container missing a limit makes that resource undefined for the whole pod:
// a percentage against a partial denominator would read as healthy headroom
// when it is nothing of the sort.
func (c TopCommand) podLimits(namespace string, allNamespaces bool) (map[string]podLimit, error) {
	args := []string{"get", "pods"}
	if allNamespaces {
		args = append(args, "-A")
	} else {
		args = append(args, "-n", namespace)
	}
	args = append(args, "-o", "json")
	raw, err := c.Kubectl.Run(args)
	if err != nil {
		return nil, err
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Resources struct {
						Limits map[string]string `json:"limits"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}

	limits := make(map[string]podLimit, len(list.Items))
	for _, item := range list.Items {
		if len(item.Spec.Containers) == 0 {
			continue
		}
		cpuTotal := resource.NewQuantity(0, resource.DecimalSI)
		memTotal := resource.NewQuantity(0, resource.BinarySI)
		cpuDefined, memDefined := true, true
		for _, container := range item.Spec.Containers {
			if value, ok := container.Resources.Limits["cpu"]; ok {
				if quantity, err := resource.ParseQuantity(value); err == nil {
					cpuTotal.Add(quantity)
				}
			} else {
				cpuDefined = false
			}
			if value, ok := container.Resources.Limits["memory"]; ok {
				if quantity, err := resource.ParseQuantity(value); err == nil {
					memTotal.Add(quantity)
				}
			} else {
				memDefined = false
			}
		}
		entry := podLimit{}
		if cpuDefined {
			entry.CPU = cpuTotal
		}
		if memDefined {
			entry.Memory = memTotal
		}
		limits[item.Metadata.Namespace+"/"+item.Metadata.Name] = entry
	}
	return limits, nil
}

// percentCell renders usage as a percentage of a limit, or an em dash when
// there is no limit to measure against.
func percentCell(usage string, limit *resource.Quantity) string {
	if limit == nil || limit.IsZero() {
		return "—"
	}
	quantity, err := resource.ParseQuantity(usage)
	if err != nil {
		return "—"
	}
	// Compared as scaled integers: milli-CPU keeps sub-core usage from
	// truncating to zero, and byte counts are exact at that scale.
	used := quantity.ScaledValue(resource.Milli)
	total := limit.ScaledValue(resource.Milli)
	if total == 0 {
		return "—"
	}
	return strconv.Itoa(int(used*100/total)) + "%"
}

// topPageRows re-parses the already-indexed (and, for nodes, relabeled)
// table text into page rows for --html. There is no richer domain struct
// behind a top listing the way diagnostics.Report/scanner.ImageScan back
// diag/scan's pages — this table text already is the whole of the data —
// so this builds web.TopRow directly rather than converting from some
// intermediate type. Degrades gracefully when a column is absent (a
// --no-limits listing has no CPU%/MEM% columns): that column's TopRow
// fields stay at their zero value, which the template/grid render as
// blank/"—".
func topPageRows(indexed index.Table) []web.TopRow {
	if !indexed.Indexable() {
		return nil
	}
	headers, rows := indexed.Headers, indexed.Rows
	nameIdx := indexOfHeader(headers, "NAME")
	if nameIdx < 0 {
		return nil
	}
	indexIdx := indexOfHeader(headers, "X")
	namespaceIdx := indexOfHeader(headers, "NAMESPACE")
	cpuIdx := indexOfHeader(headers, "CPU(cores)")
	memIdx := indexOfHeader(headers, "MEMORY(bytes)")
	cpuPctIdx := indexOfHeader(headers, "CPU%")
	memPctIdx := indexOfHeader(headers, "MEM%")

	pageRows := make([]web.TopRow, len(rows))
	for i, row := range rows {
		pageRow := web.TopRow{Name: row[nameIdx]}
		if indexIdx >= 0 {
			if n, err := strconv.Atoi(row[indexIdx]); err == nil {
				pageRow.Index = n
			}
		}
		if namespaceIdx >= 0 {
			pageRow.Namespace = row[namespaceIdx]
		}
		if cpuIdx >= 0 {
			pageRow.CPU = row[cpuIdx]
		}
		if memIdx >= 0 {
			pageRow.Memory = row[memIdx]
		}
		if cpuPctIdx >= 0 {
			pageRow.CPUPct = usageCell(row[cpuPctIdx], "cpu")
		}
		if memPctIdx >= 0 {
			pageRow.MemPct = usageCell(row[memPctIdx], "memory")
		}
		pageRows[i] = pageRow
	}
	return pageRows
}

// usageCell parses a "NN%" cell (or "—" for unknown) into a page-ready
// Usage, reusing web.NewUsage's classification so there is one coloring
// rule, not a second one duplicated here.
func usageCell(cell, kind string) web.Usage {
	pct, err := strconv.Atoi(strings.TrimSuffix(cell, "%"))
	if err != nil {
		return web.Usage{}
	}
	return web.NewUsage(pct, kind)
}

func indexOfHeader(headers []string, name string) int {
	for i, header := range headers {
		if header == name {
			return i
		}
	}
	return -1
}
