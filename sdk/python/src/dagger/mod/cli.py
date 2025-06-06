# ruff: noqa: BLE001
"""Command line interface for the dagger extension runtime."""

import importlib
import importlib.metadata
import importlib.util
import logging
import os
import sys
import typing

from opentelemetry import trace
from opentelemetry.trace.status import Status, StatusCode

from dagger import telemetry
from dagger.mod._exceptions import UserError
from dagger.mod._module import Module

logger = logging.getLogger(__name__)

ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = typing.cast(str, __package__)

IMPORT_PKG = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")
MAIN_OBJECT = os.getenv("DAGGER_MAIN_OBJECT", "Main")


def app():
    """Entrypoint for a Python Dagger module."""
    telemetry.initialize()

    # TODO: OTel logging using the OTLP exporter isn't working, or at least
    # not showing in TUI/Cloud, so adding the `logging.StreamHandler` to
    # the root logger to get logs written to stderr. Would be best to get
    # OTel logging working properly though, because it saves a bunch of
    # useful, structured data.
    logging.basicConfig(
        format="%(levelname)s %(name)s %(message)s",
        level=logging.WARNING,
        stream=sys.stderr,
    )

    mod = load_module()

    try:
        return mod()
    except Exception as e:
        span = trace.get_current_span()
        if span.get_span_context().is_valid:
            span.record_exception(e)
            span.set_status(
                Status(
                    status_code=StatusCode.ERROR,
                    description=f"{type(e).__name__}: {e}",
                )
            )
        return 1
    finally:
        telemetry.shutdown()


def load_module() -> Module:
    """Load the dagger.Module instance via the main object entry point."""
    try:
        cls: type = get_entry_point().load()
    except Exception as e:
        msg = "Code with Dagger functions could not be imported"
        raise UserError(msg) from e
    try:
        return cls.__dagger_module__
    except AttributeError as e:
        msg = "The main object must be a class decorated with @dagger.object_type"
        raise UserError(msg) from e


def get_entry_point() -> importlib.metadata.EntryPoint:
    """Get the entry point for the main object."""
    sel = importlib.metadata.entry_points(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
    )
    if ep := next(iter(sel), None):
        return ep

    import_pkg = IMPORT_PKG

    # Fallback for modules that still use the "main" package name.
    if not importlib.util.find_spec(import_pkg):
        import_pkg = "main"

        if not importlib.util.find_spec(import_pkg):
            msg = (
                "Main object not found. You can configure it explicitly by adding "
                "an entry point to your pyproject.toml file. For example:\n"
                "\n"
                f'[project.entry-points."{ENTRY_POINT_GROUP}"]\n'
                f"{ENTRY_POINT_NAME} = '{IMPORT_PKG}:{MAIN_OBJECT}'\n"
            )
            raise UserError(msg)

    return importlib.metadata.EntryPoint(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
        value=f"{import_pkg}:{MAIN_OBJECT}",
    )
