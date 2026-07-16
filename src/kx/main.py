import re
import json
from dataclasses import asdict
from typing import Optional

import typer
import typer.rich_utils
from typer.core import TyperCommand

from kx import console
from kx.commands.back import BackCommand
from kx.commands.delete import DeleteCommand
from kx.commands.diagnostic import DiagnosticCommand
from kx.commands.drop import DropCommand
from kx.commands.labels import LabelsCommand
from kx.commands.describe import DescribeCommand
from kx.commands.edit import EditCommand
from kx.commands.forward import ForwardCommand
from kx.commands.events import EventsCommand
from kx.commands.exec import ExecCommand
from kx.commands.get import GetCommand
from kx.commands.logs import LogsCommand
from kx.commands.port_forward import PortForwardCommand
from kx.commands.namespace import NamespaceCommand
from kx.commands.rollout import RolloutAction, RolloutCommand
from kx.commands.scale import ScaleCommand
from kx.commands.state import StateCommand
from kx.commands.theme import ThemeCommand
from kx.commands.tree import TreeCommand
from kx.commands.yaml import YamlCommand
from kx.config import load_config, save_theme
from kx.diagnostics import DiagnosticsService
from kx.errors import handle_errors
from kx.events import EventsService
from kx.graph import build_indexed_tree, build_tree
from kx.index import IndexService
from kx.kubectl import KubectlService
from kx.state import StateService


class StyledCommand(TyperCommand):
    def __init__(self, *args, **kwargs):
        kwargs.pop("rich_markup_mode", None)
        kwargs.pop("rich_help_panel", None)
        super().__init__(*args, **kwargs)

    def get_help(self, ctx: typer.Context) -> str:
        console.print_command_help(ctx)
        return ""


def _styled_error(error) -> None:
    if error.__class__.__name__ == "NoArgsIsHelpError":
        return
    msg = re.sub(r"(\bDid you mean [^\?]+\?) \1", r"\1", error.format_message())
    console.print_error(msg)


typer.rich_utils.rich_format_error = _styled_error  # type: ignore[assignment]

app = typer.Typer(
    add_help_option=False,
    add_completion=False,
)


# kx --help groups commands into these sections (definition order within each).
_HELP_SECTIONS = (
    (
        "Resources",
        (
            "get",
            "describe",
            "events",
            "logs",
            "labels",
            "yaml",
            "delete",
            "edit",
            "exec",
            "tree",
            "rollout",
            "scale",
            "port-forward",
            "diagnostic",
            "namespace",
        ),
    ),
    ("History", ("state", "drop", "back", "forward")),
    ("Configuration", ("theme",)),
)


def _kx_version() -> str:
    from importlib.metadata import PackageNotFoundError, version

    try:
        return version("kx-cli")
    except PackageNotFoundError:
        return "dev"


@app.callback(invoke_without_command=True)
def callback(
    ctx: typer.Context,
    no_color: bool = typer.Option(False, "--no-color", help="Disable styled output."),
    show_version: bool = typer.Option(
        False, "--version", is_eager=True, help="Show the kx version and exit."
    ),
    show_help: bool = typer.Option(
        False, "--help", is_eager=True, help="Show this message and exit."
    ),
) -> None:
    if no_color:
        console.configure(plain=True, theme=_config.theme)
        console.install_traceback()
    if show_version:
        console.print_version(_kx_version())
        raise typer.Exit()
    if show_help or ctx.invoked_subcommand is None:
        docs = {
            name: cmd.get_short_help_str(limit=55)
            for name, cmd in ctx.command.commands.items()
            if not cmd.hidden
        }
        sections = []
        for title, names in _HELP_SECTIONS:
            rows = [(name, docs.pop(name)) for name in names if name in docs]
            if rows:
                sections.append((title, rows))
        if docs:
            sections.append(("Other", list(docs.items())))
        console.print_help(sections)
        raise typer.Exit()


_config = load_config()
console.configure(plain=_config.no_color, theme=_config.theme)
console.install_traceback()
_kubectl = KubectlService()
_state = StateService(max_history=_config.max_history)
_events = EventsService()
_index = IndexService()
_diagnostics = DiagnosticsService(events=_events)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def get(
    ctx: typer.Context,
    resource: str,
    match: Optional[str] = typer.Option(
        None, "--match", "-m", help="Match by name (substring, case-insensitive)"
    ),
):
    """List resources and assign index numbers for use with other commands."""
    command = GetCommand(kubectl=_kubectl, state=_state, index=_index)
    with console.status(f"fetching {resource}"):
        result = command.execute(resource, match, ctx.args)
    all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in ctx.args)
    if all_namespaces:
        namespace = "all namespaces"
    else:
        try:
            namespace = _state.load().namespace
        except RuntimeError:
            namespace = "default"
    console.render_indexed_table(result, resource, namespace)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def describe(ctx: typer.Context, indexes: list[int]):
    """Show full kubectl describe output for one or more indexed resources."""
    command = DescribeCommand(state=_state, kubectl=_kubectl)
    for index in indexes:
        name, ns, kind = _state.fields(index)
        console.print_banner(kind, name, namespace=ns)
        command.execute(index, ctx.args)


@app.command(cls=StyledCommand)
@handle_errors
def events(indexes: list[int]):
    """Show Kubernetes events for one or more indexed resources."""
    command = EventsCommand(state=_state, events=_events)
    for index in indexes:
        name, ns, kind = _state.fields(index)
        with console.status("fetching events"):
            result = command.execute(index)
        if result.strip() == "No events found":
            count = 0
        else:
            count = len([line for line in result.splitlines() if line.strip()])
        extra = f"{count} {'item' if count == 1 else 'items'}" if count else ""
        console.print_banner(kind, name, namespace=ns, extra=extra)
        console.render_events_table(result)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def logs(ctx: typer.Context, index: int):
    """Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = LogsCommand(state=_state, kubectl=_kubectl)
    command.execute(index, ctx.args)


@app.command(cls=StyledCommand)
@handle_errors
def labels(
    indexes: list[int],
    selector: bool = typer.Option(
        False, "--selector", "-s", help="Output as a copy-pastable label selector"
    ),
):
    """Show labels for one or more indexed resources; --selector formats output as a label selector."""
    command = LabelsCommand(state=_state, kubectl=_kubectl)
    for position, index in enumerate(indexes):
        with console.status("fetching labels"):
            label_map = command.execute(index)
        name, ns, kind = _state.fields(index)
        count = len(label_map)
        extra = f"{count} {'item' if count == 1 else 'items'}"
        if position > 0:
            console.print_raw("")
        console.print_banner(kind, name, namespace=ns, extra=extra)
        if selector:
            console.print_raw(
                ",".join(f"{key}={value}" for key, value in label_map.items())
            )
        else:
            console.render_labels(label_map)


@app.command(cls=StyledCommand)
@handle_errors
def yaml(
    indexes: list[int],
    show: Optional[str] = typer.Option(
        None,
        "--show",
        help="Comma-separated top-level YAML fields to display (e.g. metadata,spec)",
    ),
):
    """Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields."""
    command = YamlCommand(state=_state, kubectl=_kubectl)
    fields = [field.strip() for field in show.split(",")] if show else None
    for index in indexes:
        name, ns, kind = _state.fields(index)
        console.print_banner(kind, name, namespace=ns)
        with console.status("fetching manifest"):
            manifest = command.execute(index, fields)
        console.print_raw(manifest)


@app.command(cls=StyledCommand)
@handle_errors
def delete(
    indexes: list[int],
    yes: bool = typer.Option(False, "--yes", "-y", help="Skip confirmation prompt"),
):
    """Delete one or more indexed resources (prompts for confirmation unless --yes)."""
    command = DeleteCommand(
        state=_state,
        kubectl=_kubectl,
        confirm=console.confirm,
    )
    for index in indexes:
        console.print_success(command.execute(index, yes))


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def edit(ctx: typer.Context, index: int):
    """Open an indexed resource in your editor via kubectl edit."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = EditCommand(state=_state, kubectl=_kubectl)
    command.execute(index, ctx.args)


@app.command(
    name="exec",
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def exec_cmd(
    ctx: typer.Context,
    index: int,
    cmd: list[str] = typer.Argument(
        default=None, help="Command to run (default: bash with sh fallback)"
    ),
):
    """Open an interactive shell in an indexed pod (bash, falling back to sh)."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = ExecCommand(state=_state, kubectl=_kubectl, shells=_config.shells)
    command.execute(index, cmd, ctx.args)


@app.command(cls=StyledCommand)
@handle_errors
def tree(
    index: int,
    indexed: bool = typer.Option(
        False, "--index", "-i", help="Assign indexes to tree nodes and update state"
    ),
):
    """Show the ownership graph for an indexed resource; --index assigns indexes to tree nodes."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = TreeCommand(
        state=_state,
        kubectl=_kubectl,
        build_tree=build_tree,
        build_indexed_tree=build_indexed_tree,
    )
    with console.status("resolving ownership graph"):
        rendered = command.execute(index, indexed)
    console.print_rich(rendered)


@app.command(cls=StyledCommand)
@handle_errors
def rollout(action: RolloutAction, index: int):
    """Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = RolloutCommand(kubectl=_kubectl, state=_state)
    result = command.execute(index, action)
    if result:
        if action == RolloutAction.history:
            console.print_raw(result)
        else:
            console.print_success(result)


@app.command(cls=StyledCommand)
@handle_errors
def scale(index: int, replicas: int):
    """Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count."""
    command = ScaleCommand(kubectl=_kubectl, state=_state)
    console.print_success(command.execute(index, replicas))


@app.command(
    "port-forward",
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def port_forward(ctx: typer.Context, index: int, port: str):
    """Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service)."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns, extra=port)
    command = PortForwardCommand(kubectl=_kubectl, state=_state)
    command.execute(index, port, ctx.args)


@app.command(cls=StyledCommand)
@handle_errors
def diagnostic(index: int):
    """Run read-only health diagnostics on an indexed Deployment, StatefulSet, DaemonSet, or Pod; alias: kx diag."""
    # render_diagnostic prints the banner: the issue count is only known post-report.
    command = DiagnosticCommand(state=_state, diagnostics=_diagnostics)
    with console.status("running diagnostics"):
        report = command.execute(index)
    console.render_diagnostic(report)


@app.command(name="diag", cls=StyledCommand, hidden=True)
@handle_errors
def diagnostic_alias(index: int):
    """Alias for diagnostic."""
    command = DiagnosticCommand(state=_state, diagnostics=_diagnostics)
    with console.status("running diagnostics"):
        report = command.execute(index)
    console.render_diagnostic(report)


@app.command(cls=StyledCommand)
@handle_errors
def namespace(index: int):
    """Switch to an indexed namespace; alias: kx ns (run kx get namespaces first)."""
    command = NamespaceCommand(state=_state, kubectl=_kubectl)
    console.print_success(f"Switched to '{command.execute(index)}'")


@app.command(name="ns", cls=StyledCommand, hidden=True)
@handle_errors
def namespace_alias(index: int):
    """Alias for namespace."""
    command = NamespaceCommand(state=_state, kubectl=_kubectl)
    console.print_success(f"Switched to '{command.execute(index)}'")


@app.command(cls=StyledCommand)
@handle_errors
def theme(
    name: Optional[str] = typer.Argument(
        default=None, help="Theme name or index (from kx theme) to activate."
    ),
):
    """List available color themes or persist a choice by name or index."""
    if name is None:
        console.render_theme_list(active=_config.theme)
    else:
        command = ThemeCommand(save=save_theme)
        console.print_success(command.execute(name))


@app.command(cls=StyledCommand)
@handle_errors
def state(
    position: Optional[int] = typer.Argument(
        default=None, help="Jump to a history position."
    ),
    all_entries: bool = typer.Option(
        False, "--all", "-a", help="Show full history stack."
    ),
):
    """Show current state, jump to a history position, or list all entries with --all."""
    if all_entries:
        console.render_state_history(_state.load_history())
    elif position is not None:
        console.render_state(json.dumps(asdict(_state.navigate_to(position)), indent=2))
    else:
        console.render_state(StateCommand(state=_state).execute())


@app.command(cls=StyledCommand)
@handle_errors
def drop(position: int):
    """Remove a history entry by position (shown in kx state --all)."""
    console.render_state_history(DropCommand(state=_state).execute(position))


@app.command(cls=StyledCommand)
@handle_errors
def back():
    """Navigate to the previous kx get result."""
    console.render_state(BackCommand(state=_state).execute())


@app.command(cls=StyledCommand)
@handle_errors
def forward():
    """Navigate to the next kx get result."""
    console.render_state(ForwardCommand(state=_state).execute())


# Help-screen metadata: print_command_help reads _aliases/_examples off the
# command callback to render the Aliases and Examples sections.
diagnostic._aliases = ["kx diag"]
namespace._aliases = ["kx ns"]

get._examples = ["kx get pods", "kx get deploy -n kube-system --match api"]
describe._examples = ["kx describe 2"]
events._examples = ["kx events 2"]
logs._examples = ["kx logs 1 -f"]
labels._examples = ["kx labels 1 --selector"]
yaml._examples = ["kx yaml 1 --show metadata,spec"]
delete._examples = ["kx delete 3 --yes"]
edit._examples = ["kx edit 1"]
exec_cmd._examples = ["kx exec 1", "kx exec 1 -- env"]
tree._examples = ["kx tree 2 --index"]
rollout._examples = ["kx rollout restart 2"]
scale._examples = ["kx scale 2 5"]
port_forward._examples = ["kx port-forward 2 8080:80"]
diagnostic._examples = ["kx diag 2"]
namespace._examples = ["kx get namespaces", "kx ns 3"]
state._examples = ["kx state --all", "kx state 2"]
drop._examples = ["kx drop 2"]
back._examples = ["kx back"]
forward._examples = ["kx forward"]
theme._examples = ["kx theme", "kx theme nord", "kx theme 3"]

if __name__ == "__main__":
    app()
