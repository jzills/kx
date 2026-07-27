"""Deferred access to the Kubernetes SDK.

`import kubernetes.client` eagerly constructs ~700 generated model classes, which
measured ~325ms. Most commands (`get`, `logs`, `describe`, `delete`, …) only shell
out to kubectl and never touch the SDK, but Typer imports every command module at
startup — so an SDK import anywhere was paid on every invocation.

The SDK-backed modules therefore bind `sdk_module`/`sdk_callable` proxies at module
scope instead of importing. Call sites read unchanged (`client.CoreV1Api()`), the
import happens on first real use, and because the proxies are ordinary module
globals `mock.patch("kx.graph.client", …)` still replaces them.

A module-level `__getattr__` (PEP 562) would not work here: it is consulted only for
attribute access on the module object from outside, not for the bare global-name
lookup a function inside the module does.
"""

import importlib
import sys
from typing import Any, Callable


class _LazyModule:
    """Stands in for a module until an attribute is actually read off it."""

    def __init__(self, path: str):
        self._path = path

    def __getattr__(self, name: str) -> Any:
        # Only reached for names not already on the instance, so the cached
        # module below short-circuits every lookup after the first.
        module = self.__dict__.setdefault(
            "_module", importlib.import_module(self._path)
        )
        return getattr(module, name)

    def __repr__(self) -> str:
        state = "loaded" if "_module" in self.__dict__ else "deferred"
        return f"<lazy module {self._path!r} ({state})>"


def sdk_module(path: str) -> Any:
    """A stand-in for `path`, imported on first attribute access."""
    return _LazyModule(path)


def sdk_callable(spec: str) -> Callable[..., Any]:
    """A stand-in for the `module:attribute` callable named by `spec`."""
    module_path, _, attribute = spec.partition(":")

    def call(*args: Any, **kwargs: Any) -> Any:
        return getattr(importlib.import_module(module_path), attribute)(*args, **kwargs)

    call.__name__ = attribute
    call.__qualname__ = attribute
    return call


def loaded_api_exceptions() -> tuple[type[BaseException], ...]:
    """`(ApiException,)` if the SDK is loaded, otherwise an empty tuple.

    Usable directly in `isinstance` checks and, unpacked, in `except` clauses.
    When `kubernetes.client.exceptions` is absent from `sys.modules` nothing can
    have raised an `ApiException`, so matching nothing is correct — and it keeps
    an ordinary kubectl `RuntimeError` from paying the SDK import on the error
    path, where the exception being handled is usually not from the SDK at all.
    """
    module = sys.modules.get("kubernetes.client.exceptions")
    return (module.ApiException,) if module is not None else ()
