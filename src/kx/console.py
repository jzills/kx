import re
import json
from contextlib import nullcontext
from datetime import datetime, timezone

import typer
from rich import traceback
from rich.console import Console
from rich.markup import escape
from rich.padding import Padding
from rich.prompt import Confirm as RichConfirm
from rich.table import Table
from rich.text import Text

from kx.diagnostics import SEVERITY_PATTERN, Severity
from kx.kinds import plural_display
from kx.state import StateHistory
from kx.themes import DEFAULT_THEME, rich_theme, styles as theme_styles

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
    Severity.OK: "status.ok",
    Severity.WARNING: "status.warn",
    Severity.CRITICAL: "status.bad",
}
_VERDICT_LABEL = {
    Severity.OK: "Healthy",
    Severity.WARNING: "Warnings",
    Severity.CRITICAL: "Critical",
}


# Width for piped/redirected output, where wrapping table rows at Rich's
# 80-column non-terminal default would mangle grep/awk pipelines.
_PIPE_WIDTH = 1000


def _build_console(
    plain: bool = False, theme: str = DEFAULT_THEME, **kwargs
) -> Console:
    # The plain console still carries the theme: semantic markup like [header]
    # must resolve even when colors are stripped.
    t = rich_theme(theme)
    if plain:
        return Console(no_color=True, highlight=False, theme=t, **kwargs)
    console = Console(theme=t, highlight=False, **kwargs)
    if not console.is_terminal:
        kwargs.setdefault("width", _PIPE_WIDTH)
        return Console(no_color=True, highlight=False, theme=t, **kwargs)
    return console


_console = _build_console()


def configure(plain: bool = False, theme: str = DEFAULT_THEME) -> None:
    global _console
    _console = _build_console(plain=plain, theme=theme)


def print_success(msg: str) -> None:
    styled = re.sub(r"'([^']+)'", "[accent]'\\1'[/accent]", msg)
    _console.print(f"[success]✓[/success] {styled}")


def print_error(msg: str) -> None:
    styled = re.sub(r"'([^']+)'", "[accent]'\\1'[/accent]", msg)
    _console.print(f"[header]✗[/header] [body]{styled}[/body]")


def print_banner(kind: str, name: str, namespace: str = "", extra: str = "") -> None:
    parts = [f"{kind}/{name}"]
    if namespace:
        parts.append(namespace)
    if extra:
        parts.append(extra)
    _console.print(f"[muted]{' · '.join(parts)}[/muted]")


def status(message: str):
    """Spinner shown while waiting on the cluster; a no-op off-terminal so
    piped output and test captures never receive spinner frames."""
    if not _console.is_terminal:
        return nullcontext()
    return _console.status(f"[muted]{message}…[/muted]", spinner_style="muted")


def confirm(message: str) -> None:
    """Themed yes/no prompt that aborts unless confirmed. kind/name tokens
    (words containing '/') get the accent style, matching banners."""
    styled = re.sub(r"(\S+/\S+)", "[accent]\\1[/accent]", escape(message))
    if not RichConfirm.ask(f"[body]{styled}[/body]", console=_console, default=False):
        raise typer.Abort()


def install_traceback() -> None:
    """Render uncaught exceptions (real bugs, not handled errors) as compact
    themed tracebacks on the active console."""
    traceback.install(console=_console, show_locals=False, word_wrap=True)


def print_raw(text: str) -> None:
    _console.print(text, markup=False, highlight=False)


def print_rich(renderable) -> None:
    _console.print(renderable)


def _status_color(status: str) -> str:
    if status in _STATUS_GREEN:
        return "status.ok"
    if status in _STATUS_RED:
        return "status.bad"
    if status in _STATUS_YELLOW or "Init" in status or status == "ContainerCreating":
        return "status.warn"
    return "status.neutral"


def _print_get_caption(resource_type: str, namespace: str, count: int) -> None:
    label = "item" if count == 1 else "items"
    _console.print(
        f"[muted]{plural_display(resource_type)} · {namespace} · {count} {label}[/muted]"
    )


def render_indexed_table(text: str, resource_type: str, namespace: str) -> None:
    lines = [line for line in text.splitlines() if line.strip()]
    if not lines:
        # kubectl exits 0 with empty stdout when nothing matches ("No
        # resources found" goes to stderr) — show the caption, not silence.
        _print_get_caption(resource_type, namespace, 0)
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

    if not rows:
        _print_get_caption(resource_type, namespace, 0)
        return

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
        header_style="header",
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

    _print_get_caption(resource_type, namespace, len(rows))
    _console.print(table)


def render_events_table(text: str) -> None:
    if text.strip() == "No events found":
        _console.print("[muted]No events found[/muted]")
        return

    table = Table(
        show_header=True,
        header_style="header",
        box=None,
        padding=(0, 2),
    )
    for col in ("TYPE", "REASON", "KIND", "AGE", "MESSAGE"):
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

        try:
            age = _format_age(datetime.fromisoformat(timestamp))
        except ValueError:
            age = timestamp

        type_color = "muted" if event_type == "Normal" else "warn"
        table.add_row(
            f"[{type_color}]{event_type}[/]",
            reason,
            kind,
            f"[muted]{age}[/]",
            message,
        )

    _console.print(table)


def render_diagnostic(report) -> None:
    verdict = report.verdict
    color = _SEVERITY_COLOR[verdict]
    # The verdict rides in the banner rather than on a line of its own; its
    # color survives the banner's dim styling via the nested tag.
    status = f"[{color}]{_SEVERITY_ICON[verdict]} {_VERDICT_LABEL[verdict]}[/{color}]"
    count = len(report.findings)
    label = "issue" if count == 1 else "issues"
    print_banner(
        report.kind,
        report.name,
        namespace=report.namespace,
        extra=f"{status} · {count} {label}" if count else status,
    )

    _console.print()
    # Align "SUMMARY" with the LOGS and WARNING EVENTS section headers.
    _console.print("  [header]SUMMARY[/header]")
    if report.findings:
        # A grid keeps the summary in its own wrap region, so continuation lines
        # hang-indent under the text instead of collapsing under the icon.
        grid = Table.grid(padding=(0, 1))
        grid.add_column(width=1, no_wrap=True)
        grid.add_column(overflow="fold")
        for finding in report.findings:
            f_color = _SEVERITY_COLOR[finding.severity]
            f_icon = _SEVERITY_ICON[finding.severity]
            grid.add_row(
                Text(f_icon, style=f_color),
                Text(finding.summary, style="body"),
            )
        _console.print(Padding(grid, (0, 0, 0, 2)))
    else:
        _console.print("  [muted]No issues detected[/muted]")

    _render_pod_table(report.pods)
    _render_logs(report.pods)
    _render_warning_events(report.warning_events)


def _render_pod_table(pods) -> None:
    if not pods:
        return
    _console.print()
    table = Table(
        show_header=True,
        header_style="header",
        box=None,
        padding=(0, 2),
    )
    table.add_column("POD")
    table.add_column("PHASE")
    table.add_column("READY")
    table.add_column("RESTARTS")
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


_LOG_ERROR_TOKENS = {
    "FATAL",
    "CRITICAL",
    "ERROR",
    "ERR",
    "EXCEPTION",
    "TRACEBACK",
    "PANIC",
}


def _highlight_severity(line: str) -> str:
    """Escape a raw log line for Rich, then color its OTEL severity tokens."""

    def repl(match):
        token = match.group(0)
        color = "error" if token.upper() in _LOG_ERROR_TOKENS else "warn"
        return f"[{color}]{token}[/{color}]"

    return SEVERITY_PATTERN.sub(repl, escape(line))


def _render_logs(pods) -> None:
    entries = [
        (pod, container)
        for pod in pods
        for container in pod.containers
        if container.log_lines
    ]
    if not entries:
        return
    _console.print()
    # Align "LOGS" under the POD column header (the pod table pads content by 2).
    _console.print("  [header]LOGS[/header]")
    for pod, container in entries:
        note = "" if container.log_filtered else " · recent output"
        _console.print(f"    [muted]{pod.name}/{container.name}{note}[/muted]")
        for line in container.log_lines:
            # Padding constrains the wrap region so wrapped lines hang-indent to
            # column 6 instead of collapsing to the console's left edge.
            styled = Text.from_markup(_highlight_severity(line))
            _console.print(Padding(styled, (0, 0, 0, 6)))


def _format_age(timestamp) -> str:
    """Render an event timestamp as a compact age (e.g. "3m ago")."""
    if not isinstance(timestamp, datetime):
        return ""
    now = datetime.now(timezone.utc) if timestamp.tzinfo else datetime.now()
    seconds = int((now - timestamp).total_seconds())
    if seconds < 0:
        return "just now"
    for unit, size in (("d", 86400), ("h", 3600), ("m", 60)):
        if seconds >= size:
            return f"{seconds // size}{unit} ago"
    return f"{seconds}s ago"


def _render_warning_events(events) -> None:
    _console.print()
    if not events:
        _console.print("[muted]No warning events[/muted]")
        return
    # Align "WARNING EVENTS" under the POD column header (content is padded by 2).
    _console.print("  [header]WARNING EVENTS[/header]")
    for event in events:
        meta = [f"{event.kind}/{event.name}", f"×{event.count}"]
        age = _format_age(event.last_timestamp)
        if age:
            meta.append(age)
        _console.print()
        _console.print(
            f"    [{_status_color(event.reason)}]{event.reason}[/]"
            f"[muted] · {' · '.join(meta)}[/muted]"
        )
        # Padding constrains the wrap region so wrapped lines hang-indent to
        # column 6 instead of collapsing to the console's left edge.
        _console.print(Padding(Text(event.message), (0, 0, 0, 6)))


def print_command_help(ctx) -> None:
    from typer.core import TyperArgument, TyperOption

    cmd = ctx.command
    _console.print()
    _console.print(f"[header]{ctx.command_path}[/header]")
    if cmd.help:
        _console.print(f"[muted]{cmd.help}[/muted]")
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
    _console.print(f"[muted]Usage[/muted]  {usage}", highlight=False)

    if args:
        _console.print()
        _console.print("[header]Arguments[/header]")
        for arg in args:
            label = "required" if arg.required else "optional"
            _console.print(
                f"  [body]{arg.human_readable_name:<20}[/body]  [muted]{label}[/muted]"
            )

    _console.print()
    _console.print("[header]Options[/header]")
    for opt in opts:
        names = "  ".join(opt.opts)
        _console.print(f"  [body]{names:<20}[/body]  [muted]{opt.help or ''}[/muted]")
    _console.print(
        f"  [body]{'--help':<20}[/body]  [muted]Show this message and exit.[/muted]"
    )

    aliases = getattr(ctx.command.callback, "_aliases", [])
    if aliases:
        _console.print()
        _console.print("[header]Aliases[/header]")
        for alias in aliases:
            _console.print(f"  [body]{alias}[/body]")

    examples = getattr(ctx.command.callback, "_examples", [])
    if examples:
        _console.print()
        _console.print("[header]Examples[/header]")
        for example in examples:
            _console.print(f"  [muted]$[/muted] {example}", highlight=False)


_KX_ART = [
    "██╗  ██╗██╗  ██╗",
    "██║ ██╔╝╚██╗██╔╝",
    "█████╔╝  ╚███╔╝ ",
    "██╔═██╗  ██╔██╗ ",
    "██║  ██╗██╔╝ ██╗",
    "╚═╝  ╚═╝╚═╝  ╚═╝",
]


def print_version(version: str) -> None:
    _console.print(f"kx {version}", markup=False, highlight=False)


def print_help(sections: list[tuple[str, list[tuple[str, str]]]]) -> None:
    _console.print()
    for line in _KX_ART:
        _console.print(line, style="header", markup=False, highlight=False)
    _console.print("[muted]kubectl, indexed.[/muted]")
    _console.print()
    _console.print(
        "[muted]Usage[/muted]  kx [OPTIONS] COMMAND [ARGS]...",
        highlight=False,
    )
    for title, commands in sections:
        _console.print()
        _console.print(f"[header]{title}[/header]")
        for name, doc in commands:
            _console.print(f"  [body]{name:<14}[/body]  [muted]{doc}[/muted]")
    _console.print()
    _console.print("[header]Options[/header]")
    _console.print(
        f"  [body]{'--no-color':<14}[/body]  [muted]Disable styled output.[/muted]"
    )
    _console.print(
        f"  [body]{'--version':<14}[/body]  [muted]Show the kx version and exit.[/muted]"
    )
    _console.print(
        f"  [body]{'--help':<14}[/body]  [muted]Show this message and exit.[/muted]"
    )


_SWATCH_PARTS = (
    ("✓ ok", "status.ok"),
    ("! warn", "status.warn"),
    ("✗ error", "status.bad"),
    ("header", "header"),
    ("body", "body"),
    ("muted", "muted"),
)


def render_theme_list(active: str) -> None:
    from rich.style import Style

    from kx.themes import THEMES

    count = len(THEMES)
    _console.print(
        f"[muted]Themes · {count} {'item' if count == 1 else 'items'}[/muted]"
    )
    table = Table(show_header=True, header_style="header", box=None, padding=(0, 2))
    table.add_column("X", justify="right")
    table.add_column("")
    table.add_column("THEME")
    table.add_column("PREVIEW")
    for position, name in enumerate(THEMES, start=1):
        is_active = name == active
        marker = Text("→", style="header") if is_active else Text("")
        label = Text(name, style="body" if is_active else "muted")
        # Preview each theme with its own literal style values (not the active
        # theme's semantic styles), so every row shows its own palette.
        theme = theme_styles(name)
        swatch = Text("  ").join(
            Text(sample, style=Style.parse(theme[key])) for sample, key in _SWATCH_PARTS
        )
        table.add_row(
            Text(str(position), style="body" if is_active else "muted"),
            marker,
            label,
            swatch,
        )
    _console.print(table)


def render_state_history(history: StateHistory) -> None:
    total = len(history.states)
    label = "entry" if total == 1 else "entries"
    _console.print(f"[muted]History · {total} {label}[/muted]")
    table = Table(
        show_header=True,
        header_style="header",
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
        marker = "[header]→[/header]" if is_current else ""
        unique_kinds = set(state.resources.values())
        kind_label = (
            plural_display(next(iter(unique_kinds)))
            if len(unique_kinds) == 1
            else "Mixed"
        )
        count = len(state.resources)
        row_color = "body" if is_current else "muted"
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
        _console.print("[muted]No labels[/muted]")
        return
    table = Table(
        show_header=True,
        header_style="header",
        box=None,
        padding=(0, 2),
    )
    table.add_column("LABEL")
    table.add_column("VALUE", style="muted")
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
    _console.print(f"[muted]{kind_label} · {namespace} · {count} {label}[/muted]")
    table = Table(
        show_header=True,
        header_style="header",
        box=None,
        padding=(0, 2),
    )
    table.add_column("X", justify="right")
    table.add_column("KIND")
    table.add_column("NAME")
    for index, (name, kind) in enumerate(resources.items(), start=1):
        table.add_row(str(index), str(kind), name)
    _console.print(table)
