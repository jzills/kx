package cli

import (
	"encoding/json"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/jzills/kx/internal/index"
	"github.com/jzills/kx/internal/kinds"
	"github.com/jzills/kx/internal/kubectl"
	"github.com/jzills/kx/internal/state"
)

// TopCommand lists pod resource usage, indexed like `kx get`, with usage
// percentages against each pod's limits.
type TopCommand struct {
	Kubectl kubectl.Service
	State   StateWriter
	Index   Indexer
}

// Execute returns the indexed table to display.
func (c TopCommand) Execute(filterTerm string, extraArgs []string, noLimits bool) (string, error) {
	output, err := c.Kubectl.Run(append([]string{"top", "pods"}, extraArgs...))
	if err != nil {
		return "", err
	}
	if filterTerm != "" {
		output = c.Index.Filter(output, filterTerm)
	}

	allNamespaces := allNamespaces(extraArgs)
	// --containers is a different table shape entirely, and -A is unindexed
	// anyway, so neither gets percentage columns.
	hasContainers := false
	for _, arg := range extraArgs {
		if arg == "--containers" {
			hasContainers = true
			break
		}
	}

	namespace := extractNamespace(extraArgs)
	if namespace == "" {
		namespace = c.Kubectl.CurrentNamespace()
	}
	if !allNamespaces && !hasContainers && !noLimits {
		output, err = c.withUsagePercentages(output, namespace)
		if err != nil {
			return "", err
		}
	}
	if allNamespaces {
		// Names aren't unique across namespaces — matches kx get's rule.
		return output, nil
	}

	indexed, names := c.Index.Add(output)
	if len(names) > 0 {
		var match *string
		if filterTerm != "" {
			match = &filterTerm
		}
		if extraArgs == nil {
			extraArgs = []string{}
		}
		if err := c.State.Save(state.State{
			Resources: state.NewResources(names, kinds.Pod),
			Namespace: namespace,
			// Recorded as a `get pods` query so a stale entry refreshes into a
			// listing, which is what the indexes were assigned against.
			Query: &state.Query{Resource: "pods", Args: extraArgs, Match: match},
		}); err != nil {
			return "", err
		}
	}
	return indexed, nil
}

// withUsagePercentages appends CPU%/MEM% columns computed against each pod's
// limits.
//
// Only handles `kubectl top pods`' default per-pod shape; the caller filters
// out the --containers and -A cases before this runs.
func (c TopCommand) withUsagePercentages(output, namespace string) (string, error) {
	headers, rows, nameIdx := index.ParseTable(output)
	cpuCol := indexOfHeader(headers, "CPU(cores)")
	memCol := indexOfHeader(headers, "MEMORY(bytes)")
	if headers == nil || cpuCol < 0 || memCol < 0 {
		return output, nil
	}

	limits, err := c.podLimits(namespace)
	if err != nil {
		return "", err
	}

	table := make([][]string, 0, len(rows)+1)
	table = append(table, append(append([]string{}, headers...), "CPU%", "MEM%"))
	for _, row := range rows {
		limit := limits[row[nameIdx]]
		table = append(table, append(append([]string{}, row...),
			percentCell(row[cpuCol], limit.CPU),
			percentCell(row[memCol], limit.Memory),
		))
	}
	return index.Format(table), nil
}

// podLimit is a pod's summed limits. A nil quantity means the limit is
// undefined for that resource.
type podLimit struct {
	CPU    *resource.Quantity
	Memory *resource.Quantity
}

// podLimits sums each resource's limit across a pod's containers, matching how
// `kubectl top pods` aggregates usage to the pod level.
//
// A container missing a limit makes that resource undefined for the whole pod:
// a percentage against a partial denominator would read as healthy headroom
// when it is nothing of the sort.
func (c TopCommand) podLimits(namespace string) (map[string]podLimit, error) {
	raw, err := c.Kubectl.Run([]string{"get", "pods", "-n", namespace, "-o", "json"})
	if err != nil {
		return nil, err
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
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
		limits[item.Metadata.Name] = entry
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

func indexOfHeader(headers []string, name string) int {
	for i, header := range headers {
		if header == name {
			return i
		}
	}
	return -1
}
