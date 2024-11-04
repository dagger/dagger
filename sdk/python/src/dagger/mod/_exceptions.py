import dataclasses
from functools import partial
from typing import Any

import cattrs
from rich.console import Console
from rich.panel import Panel

from dagger import DaggerError

_console = Console(stderr=True, style="red")


class ExtensionError(DaggerError):
    """Base class for all errors raised by extensions."""

    def rich_print(self):
        _console.print(
            Panel(
                str(self),
                border_style="red",
                title="Error",
                title_align="left",
            ),
            markup=False,
        )


class FatalError(ExtensionError):
    """An unrecoverable error."""


class InternalError(FatalError):
    """An error in Dagger itself."""


class UserError(FatalError):
    """An error that could be recovered in user code."""


class NameConflictError(UserError):
    """An error caused by a name conflict."""


class FunctionError(UserError):
    """An error while executing a user function."""


@dataclasses.dataclass(slots=True)
class ConversionError(Exception):
    """An error while converting data."""

    exc: Exception
    msg: str = ""
    origin: Any | None = None
    typ: type | None = None

    def __str__(self):
        return transform_error(self.exc, self.msg, self.origin, self.typ)

    def as_user(self, msg: str):
        return UserError(str(dataclasses.replace(self, msg=msg)))


def transform_error(
    exc: Exception,
    msg: str = "",
    origin: Any | None = None,
    typ: type | None = None,
) -> str:
    """Transform a cattrs error into a list of error messages."""
    path = "$"

    if origin is not None:
        path = getattr(origin, "__qualname__", "")
        if hasattr(origin, "__module__"):
            path = f"{origin.__module__}.{path}"

    fn = partial(cattrs.transform_error, path=path)

    if typ is not None:
        fn = partial(
            fn,
            format_exception=lambda e, _: cattrs.v.format_exception(e, typ),
        )

    errors = "; ".join(error.removesuffix(" @ $") for error in fn(exc))
    return f"{msg}: {errors}" if msg else errors
