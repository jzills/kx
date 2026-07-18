import functools
from typing import Callable

import typer
from kubernetes.client.exceptions import ApiException

from kx import console
from kx.refresh import RefreshService

_refresh_provider: Callable[[], RefreshService] | None = None


def set_refresh(provider: Callable[[], RefreshService]) -> None:
    """Register a factory for the RefreshService used on stale-state failures.

    A factory (not an instance) so the service is built per failure from the
    caller's current service objects — tests patch those with mocks.
    """
    global _refresh_provider
    _refresh_provider = provider


def _message(error: BaseException) -> str:
    if isinstance(error, ApiException):
        return f"Kubernetes API error: {error.status} {error.reason}"
    return str(error)


def _try_refresh(error: BaseException) -> None:
    """If the failure means the indexed resource is gone, re-run the entry's
    saved query and render the fresh list so the user can pick a new index."""
    if _refresh_provider is None:
        return
    refresh = _refresh_provider()
    if not refresh.matches(error):
        return
    try:
        recovered = refresh.recover()
    except (RuntimeError, ValueError, ApiException):
        return
    if recovered is None:
        console.print_raw("Run 'kx get <resource>' to refresh the list.")
        return
    table, resource, namespace = recovered
    console.print_raw("State was stale — refreshed, pick a new index:")
    console.render_indexed_table(table, resource, namespace)


def handle_errors(func=None, *, refresh: bool = True):
    """Render expected command failures as a styled error and exit 1.

    Wraps a Typer command so that `RuntimeError` (e.g. a non-zero kubectl exit or
    missing state), `ValueError` (unsupported kind, bad argument), and the
    Kubernetes SDK's `ApiException` are shown via `console.print_error` instead of
    surfacing as a traceback. When the error signals a stale state entry (the
    indexed resource no longer exists), the entry's saved query is re-run and the
    refreshed list rendered. Commands that don't consume an index (`get`) pass
    `refresh=False` — a NotFound from them can never mean stale state. `typer.Exit`
    and `typer.Abort` are not caught, so control-flow exits and confirmation
    aborts pass through untouched. `functools.wraps` preserves `__wrapped__` so
    Typer still reads the original signature.
    """

    def decorate(inner):
        @functools.wraps(inner)
        def wrapper(*args, **kwargs):
            try:
                return inner(*args, **kwargs)
            except (RuntimeError, ValueError, ApiException) as e:
                console.print_error(_message(e))
                if refresh:
                    _try_refresh(e)
                raise typer.Exit(1)

        return wrapper

    if func is not None:
        return decorate(func)
    return decorate
