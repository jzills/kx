import json
from decimal import Decimal

from kx.commands.get import _extract_namespace
from kx.index import IndexServiceProtocol, _parse_output
from kx.kinds import Kind
from kx.lazy import sdk_callable
from kx.kubectl import KubectlServiceProtocol
from kx.state import Query, State, StateServiceProtocol

parse_quantity = sdk_callable("kubernetes.utils:parse_quantity")


class TopCommand:
    def __init__(
        self,
        kubectl: KubectlServiceProtocol,
        state: StateServiceProtocol,
        index: IndexServiceProtocol,
    ):
        self.kubectl = kubectl
        self.state = state
        self.index = index

    def execute(
        self,
        filter_term: str | None = None,
        extra_args: list[str] | None = None,
        no_limits: bool = False,
    ) -> str:
        extra_args = extra_args or []
        output = self.kubectl.run(["top", "pods", *extra_args])
        if filter_term:
            output = self.index.filter(output, filter_term)
        all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in extra_args)
        has_containers_flag = "--containers" in extra_args
        if not all_namespaces and not has_containers_flag and not no_limits:
            namespace = (
                _extract_namespace(extra_args) or self.kubectl.current_namespace()
            )
            output = self._with_usage_percentages(output, namespace)
        if all_namespaces:
            # Names aren't unique across namespaces — matches kx get's rule.
            return output
        indexed_output, names = self.index.add(output)
        if names:
            namespace = (
                _extract_namespace(extra_args) or self.kubectl.current_namespace()
            )
            self.state.save(
                State(
                    resources={name: Kind.Pod for name in names},
                    namespace=namespace,
                    query=Query(resource="pods", args=extra_args, match=filter_term),
                )
            )
        return indexed_output

    def _with_usage_percentages(self, output: str, namespace: str) -> str:
        """Append CPU%/MEM% columns computed against each pod's resource
        limits. Only handles kubectl top pods' default per-pod shape — the
        --containers/-A cases are filtered out by the caller before this
        runs, since --containers is a different table shape entirely and
        -A is already unindexed/deprioritized."""
        headers, rows, name_idx = _parse_output(output)
        if not headers or "CPU(cores)" not in headers or "MEMORY(bytes)" not in headers:
            return output
        cpu_col = headers.index("CPU(cores)")
        mem_col = headers.index("MEMORY(bytes)")

        limits = self._pod_limits(namespace)
        new_headers = [*headers, "CPU%", "MEM%"]
        new_rows = []
        for row in rows:
            cpu_limit, mem_limit = limits.get(row[name_idx], (None, None))
            new_rows.append(
                [
                    *row,
                    _pct_cell(row[cpu_col], cpu_limit),
                    _pct_cell(row[mem_col], mem_limit),
                ]
            )

        all_rows = [new_headers, *new_rows]
        cols = list(zip(*all_rows))
        widths = [max(len(cell) for cell in col) for col in cols]
        return "\n".join(
            "  ".join(cell.ljust(widths[i]) for i, cell in enumerate(row))
            for row in all_rows
        )

    def _pod_limits(
        self, namespace: str
    ) -> dict[str, tuple[Decimal | None, Decimal | None]]:
        """Sum each resource's limit across a pod's containers — matches how
        `kubectl top pods` already aggregates usage to the pod level. A
        container missing a limit for a resource makes that resource
        undefined for the whole pod (no meaningful percentage against a
        partial denominator)."""
        raw = self.kubectl.run(["get", "pods", "-n", namespace, "-o", "json"])
        data = json.loads(raw)
        limits: dict[str, tuple[Decimal | None, Decimal | None]] = {}
        for item in data.get("items", []):
            containers = item.get("spec", {}).get("containers", [])
            if not containers:
                continue
            cpu_total = Decimal(0)
            mem_total = Decimal(0)
            cpu_defined = True
            mem_defined = True
            for container in containers:
                container_limits = container.get("resources", {}).get("limits") or {}
                if "cpu" in container_limits:
                    cpu_total += parse_quantity(container_limits["cpu"])
                else:
                    cpu_defined = False
                if "memory" in container_limits:
                    mem_total += parse_quantity(container_limits["memory"])
                else:
                    mem_defined = False
            limits[item["metadata"]["name"]] = (
                cpu_total if cpu_defined else None,
                mem_total if mem_defined else None,
            )
        return limits


def _pct_cell(usage_str: str, limit: Decimal | None) -> str:
    if limit is None or limit == 0:
        return "—"
    usage = parse_quantity(usage_str)
    return f"{int((usage / limit) * 100)}%"
