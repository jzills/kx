import re
import json

from rich.console import Console
from rich.table import Table

from kx.diagnostics import Severity
from kx.kinds import plural_display
from kx.state import StateHistory

COLOR_HEADER = "#3fb950"
COLOR_DIM = "#7d8590"
COLOR_BODY = "#e6edf3"
COLOR_ERROR = "#f85149"
COLOR_WARNING = "#e3b341"

_STATUS_GREEN = {
    "Running",
    "Active",
    "Bound",
    "Available",
    "Healthy",
    "Completed",
    "Succeeded",
}
_STATUS_YELLOW = {"Pending", "Terminating", "Unknown"}
_STATUS_RED = {
    "Error",
    "CrashLoopBackOff",
    "OOMKilled",
    "Failed",
    "Evicted",
    "ImagePullBackOff",
    "ErrImagePull",
    "InvalidImageName",
}

_SEVERITY_ICON = {Severity.OK: "✓", Severity.WARNING: "!", Severity.CRITICAL: "✗"}
_SEVERITY_COLOR = {
    Severity.OK: COLOR_HEADER,
    Severity.WARNING: COLOR_WARNING,
    Severity.CRITICAL: COLOR_ERROR,
}
_VERDICT_LABEL = {
    Severity.OK: "Healthy",
    Severity.WARNING: "Warnings",
    Severity.CRITICAL: "Critical",
}

_console = Console(force_terminal=True)


def configure(plain: bool) -> None:
    global _console
    _console = (
        Console(no_color=True, highlight=False)
        if plain
        else Console(force_terminal=True)
    )


def print_success(msg: str) -> None:
    _console.print(f"[bold {COLOR_HEADER}]✓[/bold {COLOR_HEADER}] {msg}")


def print_error(msg: str) -> None:
    styled = re.sub(r"'([^']+)'", f"[{COLOR_HEADER}]'\\1'[/{COLOR_HEADER}]", msg)
    _console.print(
        f"[bold {COLOR_HEADER}]✗[/bold {COLOR_HEADER}] [{COLOR_BODY}]{styled}[/{COLOR_BODY}]"
    )


def print_banner(kind: str, name: str, namespace: str = "", extra: str = "") -> None:
    parts = [f"{kind}/{name}"]
    if namespace:
        parts.append(namespace)
    if extra:
        parts.append(extra)
    _console.print(f"[{COLOR_DIM}]{' · '.join(parts)}[/{COLOR_DIM}]")


def print_raw(text: str) -> None:
    _console.print(text, markup=False, highlight=False)


def print_rich(renderable) -> None:
    _console.print(renderable)


def _status_color(status: str) -> str:
    if status in _STATUS_GREEN:
        return COLOR_HEADER
    if status in _STATUS_RED:
        return COLOR_ERROR
    if status in _STATUS_YELLOW or "Init" in status or status == "ContainerCreating":
        return COLOR_WARNING
    return COLOR_BODY


def render_indexed_table(text: str, resource_type: str, namespace: str) -> None:
    lines = [line for line in text.splitlines() if line.strip()]
    if not lines:
        return

    header_line = lines[0]
    first_col = header_line.split()[0] if header_line.split() else ""
    if first_col != "X":
        _console.print(text, markup=False, highlight=False)
        return

    spans = [(m.start(), m.end()) for m in re.finditer(r"\S+\s*", header_line)]
    headers = [header_line[start:end].strip() for start, end in spans]

    rows = []
    for line in lines[1:]:
        cols = [line[start:end].strip() for start, end in spans]
        if cols:
            rows.append(cols)

    restarts_col = headers.index("RESTARTS") if "RESTARTS" in headers else -1
    if restarts_col >= 0:
        max_num_width = max(
            (
                len(m.group(1))
                for row in rows
                if restarts_col < len(row)
                for m in [re.match(r"(\d+)", row[restarts_col])]
                if m
            ),
            default=0,
        )
        if max_num_width:
            rows = [
                [
                    re.sub(r"^(\d+)", lambda m: m.group(1).rjust(max_num_width), cell)
                    if col_idx == restarts_col
                    else cell
                    for col_idx, cell in enumerate(row)
                ]
                for row in rows
            ]

    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    _right_aligned = {"X", "AGE"}
    for header in headers:
        table.add_column(
            header, justify="right" if header in _right_aligned else "left"
        )

    status_col = headers.index("STATUS") if "STATUS" in headers else -1

    for row in rows:
        styled = []
        for index, cell in enumerate(row):
            if index == status_col:
                styled.append(f"[{_status_color(cell)}]{cell}[/]")
            else:
                styled.append(cell)
        table.add_row(*styled)

    count = len(rows)
    label = "item" if count == 1 else "items"
    _console.print(
        f"[{COLOR_DIM}]{plural_display(resource_type)} · {namespace} · {count} {label}[/{COLOR_DIM}]"
    )
    _console.print(table)


def render_events_table(text: str) -> None:
    if text.strip() == "No events found":
        _console.print(f"[{COLOR_DIM}]No events found[/{COLOR_DIM}]")
        return

    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    for col in ("TYPE", "REASON", "KIND", "TIMESTAMP", "MESSAGE"):
        table.add_column(col)

    for line in text.splitlines():
        if not line.strip():
            continue
        event_type = line[0:8].strip()
        reason = line[9:39].strip()
        kind = line[40:50].strip()
        rest = line[51:]
        parts = rest.split(" ", 2)
        timestamp = (
            f"{parts[0]} {parts[1]}" if len(parts) >= 2 else (parts[0] if parts else "")
        )
        message = parts[2] if len(parts) >= 3 else ""

        type_color = COLOR_DIM if event_type == "Normal" else COLOR_WARNING
        table.add_row(
            f"[{type_color}]{event_type}[/]",
            reason,
            kind,
            f"[{COLOR_DIM}]{timestamp}[/]",
            message,
        )

    _console.print(table)


def render_diagnostic(report) -> None:
    verdict = report.verdict
    color = _SEVERITY_COLOR[verdict]
    icon = _SEVERITY_ICON[verdict]
    count = len(report.findings)
    label = "issue" if count == 1 else "issues"
    suffix = f" — {count} {label} found" if count else ""
    _console.print(
        f"[bold {color}]{icon} {_VERDICT_LABEL[verdict]}[/bold {color}]"
        f"[{COLOR_DIM}]{suffix}[/{COLOR_DIM}]"
    )

    _console.print()
    if report.findings:
        for finding in report.findings:
            f_color = _SEVERITY_COLOR[finding.severity]
            f_icon = _SEVERITY_ICON[finding.severity]
            _console.print(
                f"  [{f_color}]{f_icon}[/{f_color}] [{COLOR_BODY}]{finding.summary}[/{COLOR_BODY}]"
            )
    else:
        _console.print(f"  [{COLOR_DIM}]No issues detected.[/{COLOR_DIM}]")

    if report.replicas is not None:
        _render_replica_health(report.replicas)
    _render_pod_table(report.pods)
    _render_warning_events(report.warning_events)


def _replica_color(value: int, desired: int) -> str:
    if value >= desired:
        return COLOR_HEADER
    if value == 0 and desired > 0:
        return COLOR_ERROR
    return COLOR_WARNING


def _render_replica_health(replicas) -> None:
    _console.print()
    ready = f"[{_replica_color(replicas.ready, replicas.desired)}]{replicas.ready}[/]"
    available = f"[{_replica_color(replicas.available, replicas.desired)}]{replicas.available}[/]"
    parts = [
        f"Desired {replicas.desired}",
        f"Ready {ready}",
        f"Available {available}",
        f"Updated {replicas.updated}",
    ]
    if replicas.generation is not None or replicas.observed_generation is not None:
        parts.append(f"Gen {replicas.generation}/{replicas.observed_generation}")
    _console.print(f"[{COLOR_DIM}]{' · '.join(parts)}[/{COLOR_DIM}]")


def _render_pod_table(pods) -> None:
    if not pods:
        return
    _console.print()
    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    table.add_column("POD")
    table.add_column("PHASE")
    table.add_column("READY", justify="right")
    table.add_column("RESTARTS", justify="right")
    table.add_column("CONTAINER")
    table.add_column("STATE")
    table.add_column("REASON")

    for pod in pods:
        ready = f"{pod.ready_containers}/{pod.total_containers}"
        phase = f"[{_status_color(pod.phase)}]{pod.phase}[/]"
        if not pod.containers:
            table.add_row(pod.name, phase, ready, "", "", "", "")
            continue
        for offset, container in enumerate(pod.containers):
            reason = container.waiting_reason or container.terminated_reason or ""
            table.add_row(
                pod.name if offset == 0 else "",
                phase if offset == 0 else "",
                ready if offset == 0 else "",
                str(container.restart_count),
                container.name,
                f"[{_status_color(container.state)}]{container.state}[/]",
                f"[{_status_color(reason)}]{reason}[/]" if reason else "",
            )
    _console.print(table)


def _render_warning_events(events) -> None:
    _console.print()
    if not events:
        _console.print(f"[{COLOR_DIM}]No warning events.[/{COLOR_DIM}]")
        return
    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    table.add_column("REASON")
    table.add_column("KIND/NAME")
    table.add_column("COUNT", justify="right")
    table.add_column("LAST")
    table.add_column("MESSAGE")
    for event in events:
        table.add_row(
            f"[{_status_color(event.reason)}]{event.reason}[/]",
            f"{event.kind}/{event.name}",
            str(event.count),
            f"[{COLOR_DIM}]{event.last_timestamp or ''}[/]",
            event.message,
        )
    _console.print(table)


def print_command_help(ctx) -> None:
    from typer.core import TyperArgument, TyperOption

    cmd = ctx.command
    _console.print()
    _console.print(f"[bold {COLOR_HEADER}]{ctx.command_path}[/bold {COLOR_HEADER}]")
    if cmd.help:
        _console.print(f"[{COLOR_DIM}]{cmd.help}[/{COLOR_DIM}]")
    _console.print()
    _console.rule(style=COLOR_DIM)
    _console.print()

    args = [param for param in cmd.params if isinstance(param, TyperArgument)]
    opts = [
        param
        for param in cmd.params
        if isinstance(param, TyperOption) and "--help" not in param.opts
    ]

    args_str = " ".join(arg.human_readable_name for arg in args)
    usage = f"{ctx.command_path} [OPTIONS]"
    if args_str:
        usage += f" {args_str}"
    _console.print(f"[{COLOR_DIM}]Usage[/{COLOR_DIM}]  {usage}", highlight=False)

    if args:
        _console.print()
        _console.print(f"[bold {COLOR_HEADER}]Arguments[/bold {COLOR_HEADER}]")
        for arg in args:
            label = "required" if arg.required else "optional"
            _console.print(
                f"  [{COLOR_BODY}]{arg.human_readable_name:<20}[/{COLOR_BODY}]  [{COLOR_DIM}]{label}[/{COLOR_DIM}]"
            )

    _console.print()
    _console.print(f"[bold {COLOR_HEADER}]Options[/bold {COLOR_HEADER}]")
    for opt in opts:
        names = "  ".join(opt.opts)
        _console.print(
            f"  [{COLOR_BODY}]{names:<20}[/{COLOR_BODY}]  [{COLOR_DIM}]{opt.help or ''}[/{COLOR_DIM}]"
        )
    _console.print(
        f"  [{COLOR_BODY}]{'--help':<20}[/{COLOR_BODY}]  [{COLOR_DIM}]Show this message and exit.[/{COLOR_DIM}]"
    )

    aliases = getattr(ctx.command.callback, "_aliases", [])
    if aliases:
        _console.print()
        _console.print(f"[bold {COLOR_HEADER}]Aliases[/bold {COLOR_HEADER}]")
        for alias in aliases:
            _console.print(f"  [{COLOR_BODY}]{alias}[/{COLOR_BODY}]")


_KX_ART = [
    "██╗  ██╗██╗  ██╗",
    "██║ ██╔╝╚██╗██╔╝",
    "█████╔╝  ╚███╔╝ ",
    "██╔═██╗  ██╔██╗ ",
    "██║  ██╗██╔╝ ██╗",
    "╚═╝  ╚═╝╚═╝  ╚═╝",
]


def print_help(commands: list[tuple[str, str]]) -> None:
    _console.print()
    for line in _KX_ART:
        _console.print(line, style=COLOR_HEADER, markup=False, highlight=False)
    _console.print(f"[{COLOR_DIM}]kubectl, indexed.[/{COLOR_DIM}]")
    _console.print()
    _console.rule(style=COLOR_DIM)
    _console.print()
    _console.print(
        f"[{COLOR_DIM}]Usage[/{COLOR_DIM}]  kx [OPTIONS] COMMAND [ARGS]...",
        highlight=False,
    )
    _console.print()
    _console.print(f"[bold {COLOR_HEADER}]Commands[/bold {COLOR_HEADER}]")
    for name, doc in commands:
        _console.print(
            f"  [{COLOR_BODY}]{name:<14}[/{COLOR_BODY}]  [{COLOR_DIM}]{doc}[/{COLOR_DIM}]"
        )
    _console.print()
    _console.print(f"[bold {COLOR_HEADER}]Options[/bold {COLOR_HEADER}]")
    _console.print(
        f"  [{COLOR_BODY}]{'--no-color':<14}[/{COLOR_BODY}]  [{COLOR_DIM}]Disable styled output.[/{COLOR_DIM}]"
    )
    _console.print(
        f"  [{COLOR_BODY}]{'--help':<14}[/{COLOR_BODY}]  [{COLOR_DIM}]Show this message and exit.[/{COLOR_DIM}]"
    )


def render_state_history(history: StateHistory) -> None:
    total = len(history.states)
    label = "entry" if total == 1 else "entries"
    _console.print(f"[{COLOR_DIM}]History · {total} {label}[/{COLOR_DIM}]")
    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    table.add_column("X", justify="right")
    table.add_column("")
    table.add_column("KIND")
    table.add_column("NAMESPACE")
    table.add_column("ITEMS", justify="right")
    for position, state in enumerate(history.states, start=1):
        is_current = (position - 1) == history.cursor
        marker = f"[bold {COLOR_HEADER}]→[/bold {COLOR_HEADER}]" if is_current else ""
        unique_kinds = set(state.resources.values())
        kind_label = (
            plural_display(next(iter(unique_kinds)))
            if len(unique_kinds) == 1
            else "Mixed"
        )
        count = len(state.resources)
        row_color = COLOR_BODY if is_current else COLOR_DIM
        table.add_row(
            f"[{row_color}]{position}[/{row_color}]",
            marker,
            f"[{row_color}]{kind_label}[/{row_color}]",
            f"[{row_color}]{state.namespace}[/{row_color}]",
            f"[{row_color}]{count}[/{row_color}]",
        )
    _console.print(table)


def render_labels(labels: dict[str, str]) -> None:
    if not labels:
        _console.print(f"[{COLOR_DIM}]No labels.[/{COLOR_DIM}]")
        return
    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    table.add_column("LABEL")
    table.add_column("VALUE", style=COLOR_DIM)
    for key, value in labels.items():
        table.add_row(key, value)
    _console.print(table)


def render_state(json_str: str) -> None:
    data = json.loads(json_str)
    namespace = data.get("namespace", "default")
    resources = data.get("resources", {})
    count = len(resources)
    label = "item" if count == 1 else "items"
    unique_kinds = set(resources.values())
    kind_label = (
        plural_display(next(iter(unique_kinds))) if len(unique_kinds) == 1 else "Mixed"
    )
    _console.print(
        f"[{COLOR_DIM}]{kind_label} · {namespace} · {count} {label}[/{COLOR_DIM}]"
    )
    table = Table(
        show_header=True,
        header_style=f"bold {COLOR_HEADER}",
        box=None,
        padding=(0, 2),
    )
    table.add_column("X", justify="right")
    table.add_column("KIND")
    table.add_column("NAME")
    for index, (name, kind) in enumerate(resources.items(), start=1):
        table.add_row(str(index), str(kind), name)
    _console.print(table)
