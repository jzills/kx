"""Startup cost guard.

The Kubernetes SDK's `kubernetes.client` package eagerly builds ~700 model
classes on import, which measured ~325ms — paid on every invocation, including
the majority of commands that only shell out to kubectl and never touch the SDK.
The SDK-backed modules therefore reach the SDK through the proxies in `kx.lazy`,
so importing the CLI must not pull it in. A subprocess is used because the SDK is
almost certainly already in `sys.modules` from other tests in this session.
"""

import re
import subprocess
import sys
from pathlib import Path

_ROOT = Path(__file__).resolve().parent.parent
_LAZY_TARGET = re.compile(r"""sdk_(?:module|callable)\(\s*["']([^"']+)["']""")

_PROBE = """
import sys
import kx.main
leaked = sorted(m for m in sys.modules if m.split(".")[0] == "kubernetes")
print("\\n".join(leaked))
"""


def test_importing_the_cli_does_not_import_the_kubernetes_sdk():
    result = subprocess.run(
        [sys.executable, "-c", _PROBE], capture_output=True, text=True, check=True
    )

    assert result.stdout.strip() == "", (
        "importing kx.main pulled in the Kubernetes SDK:\n" + result.stdout
    )


_ERROR_PATH_PROBE = """
import sys
import typer
from kx.errors import handle_errors

@handle_errors
def boom():
    raise RuntimeError("error: the server doesn't have a resource type 'chart'")

try:
    boom()
except typer.Exit as exit_:
    print("exit", exit_.exit_code)
print("sdk", any(m.split(".")[0] == "kubernetes" for m in sys.modules))
"""


def test_kubectl_failures_are_handled_without_loading_the_sdk():
    """`handle_errors` resolves `ApiException` through `loaded_api_exceptions()`.
    An ordinary kubectl failure — by far the common case — must still render and
    exit 1 without the empty-tuple fallback dragging the SDK in on the error path."""
    result = subprocess.run(
        [sys.executable, "-c", _ERROR_PATH_PROBE],
        capture_output=True,
        text=True,
        check=True,
    )

    assert "exit 1" in result.stdout
    assert "sdk False" in result.stdout


def test_sdk_backed_modules_still_expose_client():
    """The lazy attribute must resolve to the real SDK when actually used —
    `mock.patch("kx.graph.client", ...)` in the tests depends on it."""
    result = subprocess.run(
        [sys.executable, "-c", "import kx.graph; print(kx.graph.client.CoreV1Api)"],
        capture_output=True,
        text=True,
        check=True,
    )

    assert "CoreV1Api" in result.stdout


def _lazy_import_targets() -> set[str]:
    """Every module path reached via a `kx.lazy` proxy, e.g. `kubernetes.client`."""
    targets = set()
    for source in (_ROOT / "src").rglob("*.py"):
        for spec in _LAZY_TARGET.findall(source.read_text()):
            targets.add(spec.partition(":")[0])
    return targets


def test_every_lazy_import_is_declared_as_a_pyinstaller_hiddenimport():
    """PyInstaller resolves imports statically, so a module only ever named in an
    `importlib` string is omitted from the bundle — the CLI then raises
    ModuleNotFoundError at runtime for `tree`/`events`/`diagnostic`/`top`, while
    `--version` and `--help` (all the release smoke test runs) still pass. Every
    lazy target must therefore be listed in `kx.spec`'s `hiddenimports`."""
    spec = (_ROOT / "kx.spec").read_text()
    hiddenimports = re.search(r"hiddenimports=\[(.*?)\]", spec, re.S)
    assert hiddenimports, "kx.spec has no hiddenimports list"
    declared = set(re.findall(r"""["']([^"']+)["']""", hiddenimports.group(1)))

    missing = _lazy_import_targets() - declared
    assert not missing, (
        "lazily imported but absent from kx.spec hiddenimports, so they will be "
        f"missing from the released binary: {sorted(missing)}"
    )
