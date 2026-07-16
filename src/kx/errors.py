import functools

import typer

from kx import console


def handle_errors(func):
    """Render expected command failures as a styled error and exit 1.

    Wraps a Typer command so that `RuntimeError` (e.g. a non-zero kubectl exit or
    missing state) and `ValueError` (unsupported kind, bad argument) are shown via
    `console.print_error` instead of surfacing as a traceback. `typer.Exit` and
    `typer.Abort` are not caught, so control-flow exits and confirmation aborts pass
    through untouched. `functools.wraps` preserves `__wrapped__` so Typer still reads
    the original signature.
    """

    @functools.wraps(func)
    def wrapper(*args, **kwargs):
        try:
            return func(*args, **kwargs)
        except (RuntimeError, ValueError) as e:
            console.print_error(str(e))
            raise typer.Exit(1)

    return wrapper
