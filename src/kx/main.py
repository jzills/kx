import re
import json
from dataclasses import asdict
from typing import Optional

import typer
import typer.rich_utils
from typer import _click as click
from typer.core import TyperCommand, TyperGroup

from kx import console
from kx.commands.back import BackCommand
from kx.commands.delete import DeleteCommand
from kx.commands.diagnostic import DiagnosticCommand, TriageCommand
from kx.commands.drop import DropCommand
from kx.commands.annotations import AnnotationsCommand
from kx.commands.labels import LabelsCommand
from kx.commands.describe import DescribeCommand
from kx.commands.edit import EditCommand
from kx.commands.forward import ForwardCommand
from kx.commands.events import EventsCommand
from kx.commands.exec import ExecCommand
from kx.commands.get import GetCommand, _extract_namespace
from kx.commands.logs import LogsCommand
from kx.commands.metadata_write import _MetadataWriteCommand
from kx.commands.port_forward import PortForwardCommand
from kx.commands.context import ContextCommand
from kx.commands.contexts import ContextsCommand
from kx.commands.namespace import NamespaceCommand
from kx.commands.rollout import RolloutAction, RolloutCommand
from kx.commands.scale import ScaleCommand
from kx.commands.scan import ScanCommand
from kx.commands.secret import SecretCommand, to_display
from kx.commands.state import StateCommand
from kx.commands.theme import ThemeCommand
from kx.commands.top import TopCommand
from kx.commands.tree import TreeCommand
from kx.commands.yaml import YamlCommand
from kx.config import load_config, save_theme
from kx.diagnostics import DiagnosticsService
from kx.errors import handle_errors, set_refresh
from kx.events import EventsService
from kx.graph import (
    build_indexed_tree,
    build_namespace_indexed_tree,
    build_namespace_tree,
    build_tree,
)
from kx.index import IndexService
from kx.kinds import (
    Kind,
    ensure_kind,
    is_kind_spelling,
    normalize_kind,
    plural_display,
)
from kx.kubectl import KubectlService
from kx.refresh import RefreshService, StaleResourceError, is_not_found
from kx.scanner import ScannerService
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


class KindAliasGroup(TyperGroup):
    # Registered commands always win; only free kind spellings reach the alias,
    # so `kx ns 3` keeps its namespace-switch meaning.
    def resolve_command(
        self, ctx: click.Context, args: list[str]
    ) -> tuple[str | None, click.Command | None, list[str]]:
        try:
            return super().resolve_command(ctx, args)
        except click.exceptions.UsageError:
            if args and is_kind_spelling(args[0]):
                return "get", self.get_command(ctx, "get"), args
            raise


app = typer.Typer(
    cls=KindAliasGroup,
    add_help_option=False,
    add_completion=False,
    # Inherited by every subcommand's context, so `-h` works throughout even
    # though the root group renders its own help option (see `callback`).
    context_settings={"help_option_names": ["-h", "--help"]},
)


# kx --help groups commands into these sections (definition order within each).
_HELP_SECTIONS = (
    (
        "Resources",
        (
            "get",
            "secret",
            "top",
            "describe",
            "events",
            "logs",
            "labels",
            "annotations",
            "label",
            "annotate",
            "yaml",
            "delete",
            "edit",
            "exec",
            "tree",
            "rollout",
            "scale",
            "scan",
            "port-forward",
            "diagnostic",
            "namespace",
            "context",
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
        False, "--version", "-v", is_eager=True, help="Show the kx version and exit."
    ),
    show_help: bool = typer.Option(
        False, "--help", "-h", is_eager=True, help="Show this message and exit."
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
        console.print_help(sections, version=_kx_version())
        raise typer.Exit()


_config = load_config()
console.configure(plain=_config.no_color, theme=_config.theme)
console.install_traceback()
_kubectl = KubectlService()
_state = StateService(max_history=_config.max_history)
_events = EventsService()
_index = IndexService()
_scanner = ScannerService()
_diagnostics = DiagnosticsService(events=_events)
# A lambda, not an instance: it reads the module globals at call time so tests
# that patch kx.main._state/_kubectl are honored.
set_refresh(lambda: RefreshService(state=_state, kubectl=_kubectl, index=_index))


def _items_noun(count: int) -> str:
    return f"{count} {'item' if count == 1 else 'items'}"


def _render_secret(name: str, namespace: str, data: dict[str, bytes]) -> None:
    """One Secret's decoded data.

    No key count, unlike kx labels/annotations: a Secret holds a handful of
    keys, all of them visible in the table immediately below, so the count
    restates what the reader already has. The sweep's scope banner keeps its
    count — that one reports how many blocks follow, before they scroll past.

    `namespace` is blank under a sweep, whose scope banner already names it —
    print_banner drops empty parts."""
    console.print_banner(Kind.Secret, name, namespace=namespace)
    console.render_key_value_table(
        "KEY", {field: to_display(value) for field, value in data.items()}
    )


def _decode_namespace(command: SecretCommand, extra: list[str], yes: bool) -> None:
    """Every Secret in the namespace, stacked. One kubectl call covers the lot.

    Confirms first unless --yes: unlike an indexed decode, this prints every
    credential in the namespace, and it sits one flag away from the `kx secret`
    listing people run by reflex. Fetching before prompting costs nothing and
    discloses nothing, and lets the prompt name the blast radius."""
    with console.status("fetching secrets"):
        rows = command.execute_all(extra)
    count = len(rows)
    # Scope banner then per-Secret blocks, the shape kx scan's namespace sweep
    # uses; the blocks leave the namespace to this header rather than repeat it.
    namespace = (
        rows[0][1]
        if rows
        else (_extract_namespace(extra) or _kubectl.current_namespace())
    )
    console.print_scope_banner(plural_display("secret"), namespace, _items_noun(count))
    if not rows:
        return
    if not yes:
        noun = "Secret" if count == 1 else "Secrets"
        # Outside the spinner above: a prompt inside a Live region breaks input.
        console.confirm(f"Decode {count} {noun} in {namespace}?")
    for name, _ns, data in rows:
        console.print_raw("")
        _render_secret(name, "", data)


def _decode_secrets(
    resource: str,
    indexes: list[int],
    extra: list[str],
    decode: bool,
    key: Optional[str],
    yes: bool = False,
) -> None:
    """Render Secret data in plaintext: one indexed Secret, several, one key's
    raw value, or — with no index — every Secret in the namespace.

    Split out of `get` so the listing path stays untouched; decoding reads
    resources rather than listing them, so it never re-saves state."""
    if not decode:
        raise ValueError("--key requires --decode")
    expected = normalize_kind(resource)
    if expected != Kind.Secret:
        raise ValueError(f"--decode only applies to Secrets, not {expected}")
    if key is not None and len(indexes) != 1:
        raise ValueError("--key takes a single index")
    command = SecretCommand(state=_state, kubectl=_kubectl)
    if not indexes:
        _decode_namespace(command, extra, yes)
        return
    for position, index in enumerate(indexes):
        name, ns, kind = _state.fields(index)
        ensure_kind(index, name, kind, expected, f"kx get {resource}")
        try:
            with console.status("fetching secret"):
                data = command.execute(index)
        except RuntimeError as e:
            # A NotFound here means the saved index outlived the Secret; the
            # explicit error type triggers the refresh path despite refresh=False.
            if is_not_found(e):
                raise StaleResourceError(str(e)) from e
            raise
        if key is not None:
            if key not in data:
                raise ValueError(f"No key '{key}' in {kind}/{name}")
            # Raw and unwrapped so the value stays substitutable in shell.
            console.write_value(data[key])
            return
        if position > 0:
            console.print_raw("")
        _render_secret(name, ns, data)


# Shared by get and the secret command so their help text can't drift apart.
_MATCH_OPTION = typer.Option(
    None, "--match", "-m", help="Match by name (substring, case-insensitive)"
)
_DECODE_OPTION = typer.Option(
    False,
    "--decode",
    help="Show Secret data in plaintext; every Secret in the namespace when no index is given",
)
_KEY_OPTION = typer.Option(
    None, "--key", "-k", help="With --decode, print only this key's value"
)
_YES_OPTION = typer.Option(
    False,
    "--yes",
    "-y",
    help="Skip the confirmation prompt for a namespace-wide --decode",
)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors(refresh=False)
def get(
    ctx: typer.Context,
    resource: str,
    match: Optional[str] = _MATCH_OPTION,
    decode: bool = _DECODE_OPTION,
    key: Optional[str] = _KEY_OPTION,
    yes: bool = _YES_OPTION,
):
    """List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3)."""
    _get(resource, list(ctx.args), match, decode, key, yes)


def _get(
    resource: str,
    args: list[str],
    match: Optional[str],
    decode: bool = False,
    key: Optional[str] = None,
    yes: bool = False,
) -> None:
    """Shared body of `get` and the `secret` command, which delegates here so
    that shadowing the `secret` kind spelling costs none of the listing
    behaviour the alias used to provide."""
    indexes = [int(arg) for arg in args if arg.isdigit()]
    extra = [arg for arg in args if not arg.isdigit()]
    # Contexts live in kubeconfig, not on the server, so kubectl rejects
    # `get contexts`. Routing the spelling here keeps `kx get <thing>` the one
    # way to relist anything — including the hint a kind mismatch prints.
    if resource.lower() in ("context", "contexts"):
        _context(indexes[0] if indexes else None)
        return
    if decode or key is not None:
        _decode_secrets(resource, indexes, extra, decode, key, yes)
        return
    if indexes:
        expected = normalize_kind(resource)
        names = []
        namespace = None
        for idx in indexes:
            name, ns, kind = _state.fields(idx)
            ensure_kind(idx, name, kind, expected, f"kx get {resource}")
            names.append(name)
            namespace = ns
        has_namespace_flag = any(
            arg in ("-n", "--namespace") or arg.startswith("--namespace=")
            for arg in extra
        )
        if namespace and not has_namespace_flag:
            extra += ["-n", namespace]
        extra = [*names, *extra]
    command = GetCommand(kubectl=_kubectl, state=_state, index=_index)
    try:
        with console.status(f"fetching {resource}"):
            result = command.execute(resource, match, extra)
    except RuntimeError as e:
        # A NotFound after resolving an index means the state entry is stale;
        # name-based gets stay outside the refresh path (refresh=False above).
        if indexes and is_not_found(e):
            raise StaleResourceError(str(e)) from e
        raise
    all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in extra)
    if all_namespaces:
        namespace = "all namespaces"
        note = "indexes not saved for all-namespace listings — scope to a namespace (-n or kx ns) to select"
    else:
        note = None
        try:
            namespace = _state.load().namespace
        except RuntimeError:
            namespace = "default"
    console.render_indexed_table(result, resource, namespace, note=note)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors(refresh=False)
def secret(
    ctx: typer.Context,
    match: Optional[str] = _MATCH_OPTION,
    decode: bool = _DECODE_OPTION,
    key: Optional[str] = _KEY_OPTION,
    yes: bool = _YES_OPTION,
):
    """List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets."""
    _get("secret", list(ctx.args), match, decode, key, yes)


@app.command(
    name="secrets",
    hidden=True,
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors(refresh=False)
def secret_alias(
    ctx: typer.Context,
    match: Optional[str] = _MATCH_OPTION,
    decode: bool = _DECODE_OPTION,
    key: Optional[str] = _KEY_OPTION,
    yes: bool = _YES_OPTION,
):
    """Alias for secret."""
    _get("secret", list(ctx.args), match, decode, key, yes)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors(refresh=False)
def top(
    ctx: typer.Context,
    match: Optional[str] = typer.Option(
        None, "--match", "-m", help="Match by name (substring, case-insensitive)"
    ),
    no_limits: bool = typer.Option(
        False, "--no-limits", help="Skip the CPU%/MEM% columns (one fewer kubectl call)"
    ),
):
    """List CPU/memory usage for pods in the current namespace and assign index numbers, like kx get; shows usage as a percent of each pod's resource limits unless --no-limits."""
    extra = list(ctx.args)
    command = TopCommand(kubectl=_kubectl, state=_state, index=_index)
    with console.status("fetching pod usage"):
        result = command.execute(match, extra, no_limits=no_limits)
    all_namespaces = any(arg in ("-A", "--all-namespaces") for arg in extra)
    if all_namespaces:
        namespace = "all namespaces"
        note = "indexes not saved for all-namespace listings — scope to a namespace (-n or kx ns) to select"
    else:
        note = None
        try:
            namespace = _state.load().namespace
        except RuntimeError:
            namespace = "default"
    console.render_indexed_table(result, "pods", namespace, note=note)


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
    command = EventsCommand(state=_state, events=_events, kubectl=_kubectl)
    for position, index in enumerate(indexes):
        name, ns, kind = _state.fields(index)
        with console.status("fetching events"):
            rows = command.execute(index)
        count = len(rows)
        extra = f"{count} {'item' if count == 1 else 'items'}" if count else ""
        if position > 0:
            console.print_raw("")
        console.print_banner(kind, name, namespace=ns, extra=extra)
        console.render_events_table(rows)


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def logs(ctx: typer.Context, index: int):
    """Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services."""
    name, ns, kind = _state.fields(index)
    console.print_banner(kind, name, namespace=ns)
    command = LogsCommand(state=_state, kubectl=_kubectl, status=console.status)
    command.execute(index, ctx.args)


def _images_noun(count: int) -> str:
    return f"{count} {'image' if count == 1 else 'images'}"


@app.command(
    cls=StyledCommand,
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True},
)
@handle_errors
def scan(
    ctx: typer.Context,
    index: Optional[int] = typer.Argument(
        default=None,
        help="Resource index to scan; omit to scan the whole namespace.",
    ),
    engine: str = typer.Option(
        "scout", "--engine", help="Vulnerability scanner to use"
    ),
    full: bool = typer.Option(
        False,
        "--full",
        help="Stream the scanner's full output instead of the summary table",
    ),
):
    """Scan the unique container images of an indexed workload for vulnerabilities, or the whole namespace when no index is given; prints a severity summary table by default, or the raw scanner output with --full."""
    command = ScanCommand(
        state=_state, kubectl=_kubectl, scanner=_scanner, status=console.status
    )
    if index is None:
        namespace = _kubectl.current_namespace()
        images = command.collect_namespace(namespace, engine)
        console.print_scope_banner("Mixed", namespace, _images_noun(len(images)))
    else:
        name, ns, kind = _state.fields(index)
        images = command.execute(index, engine)
        console.print_banner(kind, name, namespace=ns, extra=_images_noun(len(images)))

    if full:
        for position, image in enumerate(images):
            if position > 0:
                console.print_raw("")
            console.print_section(image)
            command.scan_image(engine, image, ctx.args)
    else:
        with console.status("scanning"):
            rows = command.summarize(engine, images)
        console.render_scan_summary(rows)


def _parse_pairs(pairs: list[str]) -> dict[str, str]:
    result = {}
    for pair in pairs:
        if "=" not in pair:
            raise ValueError(f"'{pair}' is not a valid key=value pair")
        key, value = pair.split("=", 1)
        result[key] = value
    return result


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
            console.render_key_value_table("LABEL", label_map)


@app.command(cls=StyledCommand)
@handle_errors
def annotations(indexes: list[int]):
    """Show annotations for one or more indexed resources."""
    command = AnnotationsCommand(state=_state, kubectl=_kubectl)
    for position, index in enumerate(indexes):
        with console.status("fetching annotations"):
            annotation_map = command.execute(index)
        name, ns, kind = _state.fields(index)
        count = len(annotation_map)
        extra = f"{count} {'item' if count == 1 else 'items'}"
        if position > 0:
            console.print_raw("")
        console.print_banner(kind, name, namespace=ns, extra=extra)
        console.render_key_value_table("ANNOTATION", annotation_map)


@app.command(cls=StyledCommand)
@handle_errors
def label(
    index: int,
    pairs: list[str] = typer.Argument(default=None, help="key=value pairs to set"),
    remove: list[str] = typer.Option([], "--remove", help="Key to remove (repeatable)"),
    overwrite: bool = typer.Option(
        False, "--overwrite", help="Allow replacing an existing key"
    ),
):
    """Set or remove labels on an indexed resource."""
    sets = _parse_pairs(pairs or [])
    command = _MetadataWriteCommand(
        kubectl=_kubectl, state=_state, verb="label", field="labels"
    )
    with console.status("labeling"):
        result = command.execute(index, sets, remove, overwrite)
    console.print_success(result)


@app.command(cls=StyledCommand)
@handle_errors
def annotate(
    index: int,
    pairs: list[str] = typer.Argument(default=None, help="key=value pairs to set"),
    remove: list[str] = typer.Option([], "--remove", help="Key to remove (repeatable)"),
    overwrite: bool = typer.Option(
        False, "--overwrite", help="Allow replacing an existing key"
    ),
):
    """Set or remove annotations on an indexed resource."""
    sets = _parse_pairs(pairs or [])
    command = _MetadataWriteCommand(
        kubectl=_kubectl, state=_state, verb="annotate", field="annotations"
    )
    with console.status("annotating"):
        result = command.execute(index, sets, remove, overwrite)
    console.print_success(result)


@app.command(cls=StyledCommand)
@handle_errors
def yaml(
    indexes: list[int],
    show: Optional[str] = typer.Option(
        None,
        "--show",
        help="Comma-separated YAML fields to display (e.g. metadata,spec)",
    ),
):
    """Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields."""
    command = YamlCommand(state=_state, kubectl=_kubectl)
    fields = [field.strip() for field in show.split(",")] if show else None
    for position, index in enumerate(indexes):
        name, ns, kind = _state.fields(index)
        if position > 0:
            console.print_raw("")
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
        status=console.status,
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
    index: Optional[int] = typer.Argument(
        default=None,
        help="Resource index to graph; omit to graph the whole current namespace.",
    ),
    indexed: bool = typer.Option(
        False, "--index", "-i", help="Assign indexes to tree nodes and update state"
    ),
):
    """Show the ownership graph for an indexed resource, or the whole current namespace when no index is given; --index assigns indexes to tree nodes. A Namespace index graphs that namespace."""
    command = TreeCommand(
        state=_state,
        kubectl=_kubectl,
        build_tree=build_tree,
        build_indexed_tree=build_indexed_tree,
        build_namespace_tree=build_namespace_tree,
        build_namespace_indexed_tree=build_namespace_indexed_tree,
    )
    if index is None:
        namespace = _kubectl.current_namespace()
        console.print_scope_banner("Namespace", namespace)
        with console.status("resolving ownership graph"):
            rendered = command.execute_namespace(namespace, indexed)
    else:
        name, ns, kind = _state.fields(index)
        if kind == Kind.Namespace:
            console.print_scope_banner("Namespace", name)
        else:
            console.print_banner(kind, name, namespace=ns)
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
    # rollout status streams interactively and must not run inside a spinner.
    if action == RolloutAction.status:
        result = command.execute(index, action)
    else:
        with console.status(f"rollout {action.value}"):
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
    with console.status("scaling"):
        result = command.execute(index, replicas)
    console.print_success(result)


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


def _diagnostic(index: Optional[int]) -> None:
    if index is None:
        # Bare invocation: triage sweep of the current namespace. The result
        # is indexed (unhealthy rows saved as state) so kx diag <i> drills in.
        command = TriageCommand(state=_state, diagnostics=_diagnostics)
        namespace = _kubectl.current_namespace()
        with console.status(f"sweeping {namespace}"):
            result = command.execute(namespace)
        console.render_triage(result)
        return
    # render_diagnostic prints the banner: the issue count is only known post-report.
    command = DiagnosticCommand(state=_state, diagnostics=_diagnostics)
    with console.status("running diagnostics"):
        report = command.execute(index)
    console.render_diagnostic(report)


@app.command(cls=StyledCommand)
@handle_errors
def diagnostic(
    index: Optional[int] = typer.Argument(
        default=None,
        help="Resource index to diagnose; omit to triage the whole namespace.",
    ),
):
    """Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, or Pod, or triage the whole namespace when no index is given; alias: kx diag."""
    _diagnostic(index)


@app.command(name="diag", cls=StyledCommand, hidden=True)
@handle_errors
def diagnostic_alias(
    index: Optional[int] = typer.Argument(
        default=None,
        help="Resource index to diagnose; omit to triage the whole namespace.",
    ),
):
    """Alias for diagnostic."""
    _diagnostic(index)


def _namespace(index: Optional[int]) -> None:
    if index is None:
        command = GetCommand(kubectl=_kubectl, state=_state, index=_index)
        with console.status("fetching namespaces"):
            result = command.execute("namespaces", None, [])
        console.render_indexed_table(result, "namespaces", _state.load().namespace)
        return
    command = NamespaceCommand(state=_state, kubectl=_kubectl)
    with console.status("switching namespace"):
        name = command.execute(index)
    console.print_success(f"Switched to '{name}'")


def _context(index: Optional[int]) -> None:
    if index is None:
        command = ContextsCommand(kubectl=_kubectl, state=_state, index=_index)
        with console.status("fetching contexts"):
            result = command.execute()
        console.render_indexed_table(result, "Contexts", _state.load().namespace)
        return
    command = ContextCommand(state=_state, kubectl=_kubectl)
    with console.status("switching context"):
        name = command.execute(index)
    console.print_success(f"Switched to '{name}'")


@app.command(cls=StyledCommand)
@handle_errors
def namespace(
    index: Optional[int] = typer.Argument(
        default=None, help="Namespace index to switch to; omit to list namespaces."
    ),
):
    """List namespaces, or switch to an indexed one; alias: kx ns."""
    _namespace(index)


@app.command(name="ns", cls=StyledCommand, hidden=True)
@handle_errors
def namespace_alias(
    index: Optional[int] = typer.Argument(
        default=None, help="Namespace index to switch to; omit to list namespaces."
    ),
):
    """Alias for namespace."""
    _namespace(index)


@app.command(cls=StyledCommand)
@handle_errors
def context(
    index: Optional[int] = typer.Argument(
        default=None, help="Context index to switch to; omit to list contexts."
    ),
):
    """List kubeconfig contexts, or switch to an indexed one; alias: kx contexts."""
    _context(index)


@app.command(name="contexts", cls=StyledCommand, hidden=True)
@handle_errors
def context_alias(
    index: Optional[int] = typer.Argument(
        default=None, help="Context index to switch to; omit to list contexts."
    ),
):
    """Alias for context."""
    _context(index)


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
context._aliases = ["kx contexts"]
secret._aliases = ["kx secrets"]

secret._examples = [
    "kx secret",
    "kx secret 1 --decode",
    "kx secret 1 --decode -k password",
]

get._examples = [
    "kx get pods",
    "kx get deploy -n kube-system --match api",
    "kx pods",
    "kx po 3",
    "kx secret 1 --decode",
    "kx secret 1 --decode -k password",
]
top._examples = ["kx top", "kx top --sort-by=cpu", "kx top --no-limits"]
describe._examples = ["kx describe 2"]
events._examples = ["kx events 2"]
logs._examples = ["kx logs 1 -f"]
labels._examples = ["kx labels 1 --selector"]
annotations._examples = ["kx annotations 1"]
label._examples = [
    "kx label 1 env=prod",
    "kx label 1 --remove env",
    "kx label 1 env=staging --overwrite",
]
annotate._examples = ["kx annotate 1 note=x"]
yaml._examples = ["kx yaml 1 --show metadata,spec"]
delete._examples = ["kx delete 3 --yes"]
edit._examples = ["kx edit 1"]
exec_cmd._examples = ["kx exec 1", "kx exec 1 -- env"]
tree._examples = ["kx tree", "kx tree 2 --index"]
rollout._examples = ["kx rollout restart 2"]
scale._examples = ["kx scale 2 5"]
scan._examples = ["kx scan", "kx scan 1", "kx scan 1 --full", "kx scan --engine scout"]
port_forward._examples = ["kx port-forward 2 8080:80"]
diagnostic._examples = ["kx diag", "kx diag 2"]
namespace._examples = ["kx ns", "kx ns 3"]
context._examples = ["kx context", "kx context 3"]
state._examples = ["kx state --all", "kx state 2"]
drop._examples = ["kx drop 2"]
back._examples = ["kx back"]
forward._examples = ["kx forward"]
theme._examples = ["kx theme", "kx theme nord", "kx theme 3"]

if __name__ == "__main__":
    app()
