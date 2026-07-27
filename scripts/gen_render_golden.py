"""Regenerate the Go port's render parity fixtures from the Python implementation.

Captures what Rich actually emits for each listing in plain (no-color) mode, at
the wide width kx uses for non-terminal output, so the Go renderer can be
compared against it byte-for-byte. Styling is compared separately; what matters
here is layout — column widths, padding, alignment and captions.

    python scripts/gen_render_golden.py > internal/render/testdata/python_golden.json

Delete this script along with the Python implementation at cutover.
"""

import io
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

import kx.console as console  # noqa: E402
from kx.index import IndexService  # noqa: E402
from kx.state import State, StateHistory  # noqa: E402
from kx.kinds import Kind  # noqa: E402
from kx.themes import rich_theme  # noqa: E402
from rich.console import Console  # noqa: E402

# Matches _build_console's non-terminal branch: colors off, wide enough that
# table rows are never wrapped.
PIPE_WIDTH = 1000

PODS_RAW = (
    "NAME             READY   STATUS             RESTARTS   AGE\n"
    "nginx-abc-xyz    1/1     Running            0          5d\n"
    "redis-def-uvw    0/1     CrashLoopBackOff   17         3d\n"
    "api-ghi-rst      1/1     Pending            3          1h"
)

WIDE_RAW = (
    "NAME                                     READY   STATUS    AGE\n"
    "very-long-deployment-name-abcdef-12345   1/1     Running   12d\n"
    "s                                        1/1     Running   1s"
)

NO_RESTARTS_RAW = "NAME      AGE\nwidget1   5d\nwidget2   3d"


def capture(render) -> str:
    buffer = io.StringIO()
    console._console = Console(
        file=buffer,
        no_color=True,
        highlight=False,
        theme=rich_theme("github-dark"),
        width=PIPE_WIDTH,
    )
    render()
    return buffer.getvalue()


def main() -> None:
    index = IndexService()
    golden = {}

    for name, raw, resource in (
        ("pods", PODS_RAW, "pods"),
        ("wide_names", WIDE_RAW, "deploy"),
        ("no_restarts", NO_RESTARTS_RAW, "widgets.example.com"),
    ):
        indexed, _ = index.add(raw)
        golden[name] = {
            "indexed": indexed,
            "resource": resource,
            "namespace": "prod",
            "output": capture(
                lambda t=indexed, r=resource: console.render_indexed_table(t, r, "prod")
            ),
        }

    golden["empty"] = {
        "indexed": "",
        "resource": "pods",
        "namespace": "prod",
        "output": capture(lambda: console.render_indexed_table("", "pods", "prod")),
    }

    golden["single_item"] = {
        "indexed": index.add("NAME      AGE\nonly-1    5d")[0],
        "resource": "pods",
        "namespace": "default",
        "output": capture(
            lambda: console.render_indexed_table(
                index.add("NAME      AGE\nonly-1    5d")[0], "pods", "default"
            )
        ),
    }

    history = StateHistory(
        states=[
            State(resources={"web-1": Kind.Pod, "web-2": Kind.Pod}, namespace="prod"),
            State(resources={"api": Kind.Deployment}, namespace="staging"),
            State(resources={"a": Kind.Pod, "b": Kind.Service}, namespace="default"),
        ],
        cursor=1,
    )
    golden["state_history"] = {
        "output": capture(lambda: console.render_state_history(history))
    }

    golden["key_values"] = {
        "output": capture(
            lambda: console.render_key_value_table(
                "Label", {"app": "web", "tier": "frontend"}
            )
        )
    }
    golden["key_values_empty"] = {
        "output": capture(lambda: console.render_key_value_table("Label", {}))
    }

    # The widest table kx renders, and the one whose cells arrive pre-styled.
    golden["theme_list"] = {
        "output": capture(lambda: console.render_theme_list("dracula"))
    }

    print(json.dumps(golden, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
