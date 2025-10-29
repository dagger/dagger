"""Command line interface for the dagger extension runtime."""

import importlib
import importlib.metadata
import importlib.util
import logging
import os
import json
import typing

import anyio

import dagger
from dagger import telemetry
from dagger.mod._static_build import static_typedefs
from dagger.mod._exceptions import ModuleError, ModuleLoadError, record_exception
from dagger.mod._module import MAIN_OBJECT, MODULE_NAME, TYPE_DEF_FILE, Module


logger = logging.getLogger(__package__)

ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = typing.cast(str, __package__)
IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")


def app(mod: Module | None = None, register: bool = False) -> int | None:
    """Entrypoint for a Python Dagger module."""
    telemetry.initialize()
    try:
        return anyio.run(main, mod, register)
    finally:
        telemetry.shutdown()

def _find_project_root(start: str | None = None) -> str:
    cur = os.path.abspath(start or os.getcwd())
    chosen: str | None = None
    while True:
        if os.path.isdir(os.path.join(cur, "src")):
            chosen = cur
            break
        parent = os.path.dirname(cur)
        if not parent or parent == cur:
            break
        cur = parent
    return chosen or os.getcwd()

async def main(mod: Module | None = None, register: bool = False) -> int | None:
    """Async entrypoint for a Dagger module.

    Behavior change: when `register=True` and `mod is None`, perform a fast, static
    registration without importing user code or connecting to the engine.
    """
    async with await dagger.connect():
        try:
            if register and mod is None:
                project_root = _find_project_root()
                mod_id = await static_typedefs(project_root=project_root, main_name=MAIN_OBJECT)
                output = json.dumps(mod_id)
                await anyio.Path(TYPE_DEF_FILE).write_text(output)
                return 0
            if mod is None:
                mod = load_module()
            if register:
                return await mod.register()
            return await mod.serve()
        except LookupError:
            msg = (
                f"Main object with name '{MAIN_OBJECT}' not found or class not "
                "decorated with '@dagger.object_type'\n"
                f"If you believe the module name '{MODULE_NAME}' is incorrectly "
                "being converted into PascalCase, please file a bug report."
            )
            logger.error(msg)
            return 2
        except (ModuleError, dagger.QueryError) as e:
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
    try:
        return cls.__dagger_module__
    except AttributeError:
        msg = (
            "The main object must be a class decorated with @dagger.object_type, "
            f"found '{type(cls)}'"
        )
        raise ModuleLoadError(msg) from None


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
