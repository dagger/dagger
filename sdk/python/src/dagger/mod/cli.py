"""Command line interface for the dagger extension runtime."""

import importlib
import importlib.metadata
import importlib.util
import json
import logging
import os
import typing
from pathlib import Path

import anyio

import dagger
from dagger import telemetry
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


async def main(mod: Module | None = None, register: bool = False) -> int | None:
    """Async entrypoint for a Dagger module."""
    # Establishing connection early on to allow returning dag.error().
    # Note: if there's a connection error dag.error() won't be sent but
    # should be logged and the traceback shown on the function's stderr output.
    async with await dagger.connect():
        try:
            if register:
                # Use AST-based registration (doesn't require importing module)
                return await register_with_ast()

            # For invocation, we need to load the module
            if mod is None:
                mod = load_module()
            return await mod.serve()
        except (ModuleError, dagger.QueryError) as e:
            await record_exception(e)
            return 2
        except Exception as e:
            logger.exception("Unhandled exception")
            await record_exception(e)
            return 1


async def register_with_ast() -> int | None:
    """Register module types using AST analysis.

    This analyzes Python source files without importing them,
    allowing registration when dependencies are not available.
    """
    from dagger.mod._analyzer import analyze_module
    from dagger.mod._analyzer.errors import AnalysisError
    from dagger.mod._analyzer.registration import register_from_metadata

    try:
        # Find source files
        source_files = find_source_files()
        if not source_files:
            msg = (
                "No Python source files found for module. "
                f"Looking for package: {IMPORT_PKG}"
            )
            raise ModuleLoadError(msg)

        logger.debug("Found source files: %s", source_files)

        # Analyze module
        metadata = analyze_module(
            source_files=source_files,
            main_object_name=MAIN_OBJECT,
            module_name=MODULE_NAME,
        )

        logger.debug(
            "Analyzed module: %d objects, %d enums",
            len(metadata.objects),
            len(metadata.enums),
        )

        # Register with engine
        module_id = await register_from_metadata(metadata)

        # Write result to file
        output = json.dumps(module_id)
        await anyio.Path(TYPE_DEF_FILE).write_text(output)

        logger.debug("Registration complete: %s", TYPE_DEF_FILE)
    except AnalysisError as e:
        logger.exception("AST analysis failed")
        raise ModuleLoadError(str(e)) from e
    else:
        return None


def find_source_files() -> list[str]:
    """Find Python source files for the module.

    Returns a list of .py files in the module package.
    """
    spec = _find_module_spec()

    if spec is None or spec.submodule_search_locations is None:
        return []

    # Get the package directory
    search_locations = spec.submodule_search_locations
    if not search_locations:
        # Single-file module
        if spec.origin and spec.origin.endswith(".py"):
            return [spec.origin]
        return []

    # Find all .py files in the package
    source_files: list[str] = []
    for location in search_locations:
        pkg_path = Path(location)
        if pkg_path.is_dir():
            _collect_package_files(pkg_path, source_files)

    return source_files


def _find_module_spec() -> importlib.machinery.ModuleSpec | None:
    """Find the module spec, falling back to 'main' package."""
    spec = importlib.util.find_spec(IMPORT_PKG)
    if spec is None:
        spec = importlib.util.find_spec("main")
    return spec


def _collect_package_files(pkg_path: Path, source_files: list[str]) -> None:
    """Collect .py files from a package directory into source_files."""
    # Add __init__.py first if it exists
    init_file = pkg_path / "__init__.py"
    if init_file.exists():
        source_files.append(str(init_file))

    # Add other .py files
    source_files.extend(
        str(py_file)
        for py_file in pkg_path.glob("*.py")
        if py_file.name != "__init__.py"
    )

    # Add files from subdirectories (one level deep)
    for subdir in pkg_path.iterdir():
        if subdir.is_dir() and not subdir.name.startswith("_"):
            source_files.extend(str(py_file) for py_file in subdir.glob("*.py"))


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
