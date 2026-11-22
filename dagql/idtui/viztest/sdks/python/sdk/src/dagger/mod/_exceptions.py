import json
import logging
import pathlib
import re
import sys
import traceback
from collections.abc import Mapping
from typing import Any, Final

import cattrs
from opentelemetry.semconv.attributes.exception_attributes import (
    EXCEPTION_MESSAGE,
    EXCEPTION_STACKTRACE,
    EXCEPTION_TYPE,
)

from dagger import telemetry
from dagger._exceptions import DaggerError, QueryError
from dagger.mod import _api

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


FUNCTION_ERROR_MARKER: Final = "::dagger-function-error::"
"""Starts the line on which a call reports the function's error.

The generated entrypoint raises the text after it as the function's error.
"""

# A failed exec gives the entrypoint only the last 100 KiB of its stderr. JSON
# escapes a character in at most six bytes, so 16 Ki characters fit.
_REPORTED_ERROR_LIMIT: Final = 16 << 10

# The engine finds where an error comes from by these, at the message's end.
_ERROR_ORIGIN: Final = re.compile(r"\s*\[traceparent:[0-9a-f]{32}-[0-9a-f]{16}\]")


def function_error_message(exc: BaseException, *, logged: bool = False) -> str:
    """The error a failed call reports: the function's own, not its wrapper.

    An API error's message is kept whole, with the origin it names. Each
    error of a group is reported, as a task group raises them. An exception
    the function raised is named with its type, unless `logged` says its
    traceback, which names it, is already in the function's logs.
    """
    if isinstance(exc, FunctionError) and exc.__cause__ is not None:
        if logged and not isinstance(exc.__cause__, BaseExceptionGroup):
            return str(exc) or type(exc.__cause__).__name__
        exc = exc.__cause__
    if isinstance(exc, BaseExceptionGroup):
        return "; ".join(function_error_message(e) for e in exc.exceptions)
    if isinstance(exc, ModuleError | QueryError):
        return str(exc) or type(exc).__name__
    return f"{type(exc).__name__}: {exc}" if str(exc) else type(exc).__name__


FUNCTION_ERROR_PATH_ENV: Final = "DAGGER_FUNCTION_ERROR_PATH"
"""The file the engine gives a call's process to write the function's error to.

An engine that sets it fails the call with the error written there; one that
doesn't reads the FUNCTION_ERROR_MARKER line instead.
"""


class ErrorOriginFilter(logging.Filter):
    """Leave the error origins out of what a call's process logs.

    An API error's message ends with the origin the engine finds it by, such
    as `[traceparent:...]`: plumbing for the engine, which reads it from the
    error the process reports, and noise in the function's logs, a traceback
    included.
    """

    def filter(self, record: logging.LogRecord) -> bool:
        if record.exc_info and not record.exc_text:
            record.exc_text = logging.Formatter().formatException(record.exc_info)
        if record.exc_text:
            record.exc_text = _ERROR_ORIGIN.sub("", record.exc_text)
        message = record.getMessage()
        if _ERROR_ORIGIN.search(message):
            record.msg, record.args = _ERROR_ORIGIN.sub("", message), None
        return True


def _limit(message: str) -> str:
    if len(message) > _REPORTED_ERROR_LIMIT:
        origins = "".join(_ERROR_ORIGIN.findall(message[_REPORTED_ERROR_LIMIT:]))
        message = message[:_REPORTED_ERROR_LIMIT] + "..." + origins
    return message


def write_function_error(path: str, exc: BaseException) -> None:
    """Give the engine a failed call's error in the file it names.

    The engine fails the call with this error as the function's own, as with
    `record_exception`: `{"message": ..., "values": {...}}`, the message in
    full and the values `function_error_values` gives. An API error's
    message names the call that failed, so that call stays the error's
    origin; any other error comes from the function call.
    """
    # The traceback, which names the exception's type, is already in the
    # function's logs.
    message = function_error_message(exc, logged=True)
    if isinstance(exc, FunctionError) and exc.__cause__ is not None:
        # The values of the error the FunctionError wraps, and the extra
        # values it adds.
        values = {**function_error_values(exc.__cause__), **_extra_values(exc)}
    else:
        values = function_error_values(exc)
    values = {key: _json_value(value) for key, value in values.items()}
    encoded = _function_error_report(message, values)
    if len(encoded.encode()) > _FUNCTION_ERROR_LIMIT:
        # The engine reads none of a report over its limit: keep the
        # message, and the values that fit.
        message = _limit(message)
        values = {
            key: _limit(value) if isinstance(value, str) else value
            for key, value in values.items()
            if key != EXCEPTION_STACKTRACE
        }
        encoded = _function_error_report(message, values)
        if len(encoded.encode()) > _FUNCTION_ERROR_LIMIT:
            encoded = _function_error_report(message, {})
    pathlib.Path(path).write_text(encoded, encoding="utf-8")


_FUNCTION_ERROR_LIMIT: Final = 1 << 20
"""The largest error file the engine reads, in bytes."""


def _function_error_report(message: str, values: dict[str, Any]) -> str:
    return json.dumps({"message": message, "values": values}, ensure_ascii=False)


def function_error_values(exc: BaseException) -> dict[str, Any]:
    """The values of the error a failed call reports for `exc`.

    The same as `record_exception` gives its `dagger.Error`: the exception's
    type, message and stack trace, a ModuleError's `extra` values, and an API
    error's extensions, such as an ExecError's exit code and output, whole.
    The API error may be one the exception was raised from (`raise ... from`),
    as one that adds to its message is, whose values the exception's own
    come before; an exception raised while handling one is its own error.
    """
    attrs: dict[str, Any] = {**_extra_values(exc), **_exception_attributes(exc)}
    if isinstance(exc, QueryError):
        attrs.update(exc.error.extensions)
    elif (api_error := _raised_from_query_error(exc)) is not None:
        attrs = {**api_error.error.extensions, **attrs}
    return attrs


def _extra_values(exc: BaseException) -> dict[str, Any]:
    """A ModuleError's `extra` values, as the error's values."""
    if isinstance(exc, ModuleError) and exc.extra:
        return {f"extra.{key}": val for key, val in exc.extra.items()}
    return {}


def _raised_from_query_error(exc: BaseException) -> QueryError | None:
    """The API error exc was raised from, directly or not, if any."""
    seen: set[int] = set()
    cur = exc.__cause__
    while cur is not None and id(cur) not in seen:
        if isinstance(cur, QueryError):
            return cur
        seen.add(id(cur))
        cur = cur.__cause__
    return None


def _json_value(value: Any) -> Any:
    """The value if it is JSON, else its repr, as `_safe_json_dumps` does."""
    try:
        json.dumps(value)
    except (TypeError, ValueError):
        return repr(value)
    return value


def report_function_error(message: str) -> None:
    """Report a failed call's error as the last line of stderr.

    The process that runs a call has no current function call to return its
    error to, so its exec fails, and the generated entrypoint raises the
    message from this line instead of the exec's exit code. An API error's
    message names the call that failed, so that call becomes the error's
    origin, not the process. An engine that gives the process a file for the
    error (FUNCTION_ERROR_PATH_ENV) gets it there instead.
    """
    encoded = json.dumps(_limit(message), ensure_ascii=False)
    sys.stderr.write(f"\n{FUNCTION_ERROR_MARKER}{encoded}\n")
    sys.stderr.flush()


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
    if isinstance(exc, QueryError):
        msg = str(exc)
        attrs.update(exc.error.extensions)

    dag_err = _api.error(msg)
    for key, value in attrs.items():
        dag_err = dag_err.with_value(key, _safe_json_dumps(value))

    await _api.current_function_call().return_error(dag_err)

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


def _exception_attributes(exc: BaseException) -> dict[str, str]:
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
