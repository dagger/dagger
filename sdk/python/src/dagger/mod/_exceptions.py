import json
import logging
import traceback
import typing
from typing import Any

import cattrs
from opentelemetry import trace
from opentelemetry.semconv.attributes.exception_attributes import (
    EXCEPTION_MESSAGE,
    EXCEPTION_STACKTRACE,
    EXCEPTION_TYPE,
)

import dagger
from dagger import DaggerError, dag

logger = logging.getLogger(__package__)


class ModuleError(DaggerError):
    """Base class for all errors raised by modules."""

    def __init__(
        self,
        msg: str = "",
        exc: Exception | None = None,
        extra: dict[str, Any] | None = None,
        notes: tuple[str, ...] = (),
    ):
        if exc is not None and msg == "":
            msg = f"{type(exc).__name__}: {exc}"
        super().__init__(msg)
        self.original = exc
        self.extra = extra
        if hasattr(self, "add_note"):
            for note in notes:
                self.add_note(note)

    def with_log(self, exc_info=False):
        msg = "".join(traceback.format_exception_only(self))
        msg = msg.replace(__name__ + ".", "")
        logger.error(msg, exc_info=exc_info, stacklevel=2, extra=self.extra)
        return self


class ModuleLoadError(ModuleError):
    """Error while loading Python module with functions."""


class InvalidInputError(ModuleError):
    """Error while deserializing values into Python objects.

    If it happens it's probably a bug in the SDK because the API should
    validate early if input is not of expected type.
    """


class InvalidResultError(ModuleError):
    """Error while serializing Python values into JSON."""


class ObjectNotFoundError(ModuleError):
    """Parent object not found on registry."""


class RegistrationError(ModuleError):
    """An error caused by an invalid type def registration."""


class BadUsageError(ModuleError):
    """A usage error."""


class FunctionError(ModuleError):
    """An error while executing a user function."""


def transform_error(
    exc: Exception,
    msg: str = "",
    origin: Any | None = None,
    typ: type | None = None,
) -> str:
    """Transform an exception raised by cattrs into an error message."""
    kwargs = {}
    if origin is not None:
        path = getattr(origin, "__qualname__", "")
        if hasattr(origin, "__module__"):
            path = f"{origin.__module__}.{path}"

        if path:
            kwargs["path"] = path

    if msg:
        msg += ": "

    # cattrs.transform_error sets expected type as None when not a cattrs exception.
    if typ is not None and not isinstance(exc, cattrs.BaseValidationError):
        msg += cattrs.v.format_exception(exc, typ)
        if path := kwargs.get("path"):
            msg = f"{msg} @ {path}"
    else:
        msg += "; ".join(
            error.removesuffix(" $").removesuffix(" @")
            for error in cattrs.transform_error(exc, **kwargs)
        )

    return msg


async def handle_error(exc: Exception):
    """Convert a Python exception into a `dagger.Error`."""
    attributes: dict[str, typing.Any] = _exception_attributes(exc)

    if isinstance(exc, ModuleError):
        if exc.original:
            attributes.update(_exception_attributes(exc.original))
        if exc.extra:
            attributes.update(exc.extra)

    # API error with extensions added last to make sure they're not overwritten.
    if isinstance(exc, dagger.QueryError):
        attributes.update(exc.error.extensions)

    dag_err = dag.error(str(exc))
    for key, value in attributes.items():
        dag_err.with_value(key, dagger.JSON(json.dumps(value)))

    await dag.current_function_call().return_error(dag_err)

    # This would be automatic if using a custom span, but we're using the parent
    # span started by the engine for calling this function so just replicating
    # sending the event with exception details.
    span = trace.get_current_span()
    if span.get_span_context().is_valid:
        span.record_exception(exc)


def _exception_attributes(exc: Exception) -> dict[str, str]:
    message = str(exc)
    stacktrace = traceback.format_exc()
    module = type(exc).__module__
    qualname = type(exc).__qualname__
    exc_type = f"{module}.{qualname}" if module and module != "builtins" else qualname

    # Reusing OTel attribute names for consistency.
    return {
        EXCEPTION_TYPE: exc_type,
        EXCEPTION_MESSAGE: message,
        EXCEPTION_STACKTRACE: stacktrace,
    }
