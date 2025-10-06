import json
import logging
import traceback
from collections.abc import Mapping
from typing import Any

import cattrs
from opentelemetry.semconv.attributes.exception_attributes import (
    EXCEPTION_MESSAGE,
    EXCEPTION_STACKTRACE,
    EXCEPTION_TYPE,
)

import dagger
from dagger import DaggerError, dag, telemetry

logger = logging.getLogger(__package__)


class ModuleError(DaggerError):
    """Base class for all errors raised by modules.

    This class flags to the entrypoint that the error has been handled so we
    can exit cleanly, without dumping the full traceback even when it's not
    useful.

    It also allows control over what gets reported to dag.error() via the
    error message and extra values.
    """

    def __init__(self, /, *args, extra: Mapping[str, Any] | None = None):
        super().__init__(*args)
        self.extra = extra


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


def log_exception_only(
    exc: Exception,
    msg: str,
    *args,
    # Extra note to add to the end of the error message when shown on
    # the log, but not included in the dagger.Error message.
    note: str = "",
):
    """Log just this exception, without full traceback.

    Generates an error log message just for this exception, not the full
    traceback, including without any chained exceptions.

    This should be used in cases where this exception has enough information
    or when the rest of the traceback doesn't add anything particularly useful,
    so there's less noise to sort through while debugging.

    The full traceback will still be included in dag.Error() values which
    could at some point be optionally shown in the web/cloud UI at some
    point, but it's also fully available to LLM in the meantim.
    """
    if note and hasattr(exc, "add_note"):
        exc.add_note(note)
    logger.error(msg, *args, exc_info=(type(exc), exc, None))


async def record_exception(exc: Exception):
    """Convert a Python exception into a `dagger.Error`."""
    attrs: dict[str, Any] = _exception_attributes(exc)
    msg = f"{attrs[EXCEPTION_TYPE]}: {attrs[EXCEPTION_MESSAGE]}"

    if isinstance(exc, ModuleError) and exc.extra:
        extra = {f"extra.{key}": val for key, val in exc.extra.items()}
        # ModuleError extra values don't conflict with the OTel attributes
        # but prepending like this avoids a future mistake.
        attrs = {**extra, **attrs}

    # Preserve original API error so it's properly propagated.
    if isinstance(exc, dagger.QueryError):
        msg = str(exc)
        attrs.update(exc.error.extensions)

    dag_err = dag.error(msg)
    for key, value in attrs.items():
        dag_err = dag_err.with_value(key, dagger.JSON(_safe_json_dumps(value)))

    await dag.current_function_call().return_error(dag_err)

    # When an error occurs within a started span context the OTel SDK
    # automatically sends an event with details about the exception.
    # Switching to dag.Error doesn't take advantage of that and the engine
    # doesn't recreate the exception event on the parent function span.
    # Still, recording the exception manually can be useful when analyzing the
    # raw telemetry in e.g., Honeycomb.
    with telemetry.get_tracer().start_as_current_span(
        "recording Python exception",
        # TODO: even with following attribute it's still being shown in the
        # Cloud UI.
        attributes={"dagger.io/ui.internal": True},
    ) as span:
        span.record_exception(exc)


def _exception_attributes(exc: Exception) -> dict[str, str]:
    message = str(exc)
    stacktrace = "".join(traceback.format_exception(exc))
    module = type(exc).__module__
    qualname = type(exc).__qualname__
    exc_type = f"{module}.{qualname}" if module and module != "builtins" else qualname

    # hide the full `dagger.mod._exception` module path
    exc_type = exc_type.replace(__name__ + ".", "")

    # Reusing OTel attribute names for consistency.
    return {
        EXCEPTION_TYPE: exc_type,
        EXCEPTION_MESSAGE: message,
        EXCEPTION_STACKTRACE: stacktrace,
    }


def _safe_json_dumps(value: Any) -> str:
    """Safely serialize value to JSON, falling back to repr() if not serializable."""
    try:
        return json.dumps(value)
    except (TypeError, ValueError):
        # Fall back to string representation for non-serializable values
        return json.dumps(repr(value))
