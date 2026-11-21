"""Command line interface for the dagger extension runtime."""

import contextlib
import gc
import importlib
import importlib.metadata
import importlib.util
import logging
import os
import typing

import anyio

from dagger import telemetry
from dagger._exceptions import QueryError
from dagger.client._connection import connect
from dagger.client._descriptor import registering_types
from dagger.client._session import mark_module_runtime
from dagger.mod._exceptions import ModuleError, ModuleLoadError, record_exception
from dagger.mod._module import MAIN_OBJECT, Module

logger = logging.getLogger(__package__)

ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = typing.cast(str, __package__)
IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")


def app(mod: Module | None = None, register: bool = False) -> int | None:
    """Entrypoint for a Python Dagger module."""
    mark_module_runtime()
    telemetry.initialize()
    try:
        return anyio.run(main, mod, register)
    finally:
        telemetry.shutdown()


async def main(mod: Module | None = None, register: bool = False) -> int | None:
    """Async entrypoint for a Dagger module."""
    # Establishing connection early on to allow returning dag.error().
    # Note: if there's a connection error dag.error() won't be sent but
    # should be logged and the traceback shown on the function's stderr output.
    async with await connect():
        try:
            if mod is None:
                # Only an explicit registration is known before the user's
                # code is imported; serve() decides after.
                with registering_types() if register else contextlib.nullcontext():
                    mod = load_module()
            if register:
                return await mod.register()
            return await mod.serve()
        except (ModuleError, QueryError) as e:
            await record_exception(e)
            return 2
        except Exception as e:
            logger.exception("Unhandled exception")
            await record_exception(e)
            return 1


def load_module() -> Module:
    """Load the dagger.Module instance via the main object entry point."""
    ep = get_entry_point()
    try:
        cls = ep.load()
    except Exception as e:
        logger.exception(
            "Error while importing Python module '%s' with Dagger functions",
            ep.module,
        )
        raise ModuleLoadError(str(e)) from e
    _resume_gc()
    try:
        return cls.__dagger_module__
    except AttributeError:
        msg = (
            "The main object must be a class decorated with @dagger.object_type, "
            f"found '{type(cls)}'"
        )
        raise ModuleLoadError(msg) from None


def _resume_gc() -> None:
    """Turn collection back on after dagger/__init__.py paused it.

    Freezing first keeps the objects the imports made, which live for the
    whole call, out of every collection the user's code triggers.
    """
    if os.environ.get("DAGGER_MODULE") and not gc.isenabled():
        gc.freeze()
        gc.enable()


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
            raise ModuleLoadError(msg)

    return importlib.metadata.EntryPoint(
        group=ENTRY_POINT_GROUP,
        name=ENTRY_POINT_NAME,
        value=f"{import_pkg}:{MAIN_OBJECT}",
    )
