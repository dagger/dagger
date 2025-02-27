"""Command line interface for the dagger extension runtime."""

import importlib
import importlib.metadata
import importlib.util
import logging
import os
import sys
import typing

import rich.traceback
from rich.console import Console

from dagger import telemetry
from dagger.log import configure_logging
from dagger.mod._exceptions import FatalError, UserError
from dagger.mod._module import Module

ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = typing.cast(str, __package__)

IMPORT_PKG = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")
MAIN_OBJECT = os.getenv("DAGGER_MAIN_OBJECT", "Main")

errors = Console(stderr=True, style="red")
logger = logging.getLogger(__name__)


def app():
    """Entrypoint for a Python Dagger module."""
    telemetry.initialize()

    # TODO: Create custom exception hook to control exit code.
    rich.traceback.install(
        console=errors,
        show_locals=logger.isEnabledFor(logging.DEBUG),
        suppress=[
            "asyncio",
            "anyio",
        ],
    )
    try:
        load_module()()
    except FatalError as e:
        logger.exception("Fatal error")
        e.rich_print()
        sys.exit(1)
    finally:
        telemetry.shutdown()


def load_module() -> Module:
    """Load the dagger.Module instance via the main object entry point."""
    try:
        cls: type = get_entry_point().load()
    except (ModuleNotFoundError, AttributeError) as e:
        # If the main module isn't found the user won't be able to set debug level.
        # TODO: Allow setting debug level with a pyproject.toml setting.
        if not logger.isEnabledFor(logging.DEBUG):
            configure_logging(logging.DEBUG)
        msg = (
            "Main object not found. You can configure it explicitly by adding "
            "an entry point to your pyproject.toml file. For example:\n"
            "\n"
            f'[project.entry-points."{ENTRY_POINT_GROUP}"]\n'
            f"{ENTRY_POINT_NAME} = '{IMPORT_PKG}:{MAIN_OBJECT}'\n"
        )
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

    return importlib.metadata.EntryPoint(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
        value=f"{import_pkg}:{MAIN_OBJECT}",
    )
