"""Command line interface for the dagger extension runtime."""

import importlib
import importlib.metadata
import importlib.util
import logging
import os
import typing
from pathlib import Path

import anyio

import dagger
from dagger import dag, telemetry
from dagger.mod._exceptions import ModuleError, ModuleLoadError, record_exception
from dagger.mod._module import MAIN_OBJECT, Module

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
            if mod is None:
                # Detect if we're in invocation mode (has parent_name)
                # or registration mode (no parent_name)
                parent_name = await dag.current_function_call().parent_name()
                if parent_name:
                    # Invocation mode: need to load the module at runtime
                    mod = load_module_for_invocation()
                else:
                    # Registration mode: use AST analysis
                    mod = load_module_for_registration()
            if register:
                return await mod.register()
            return await mod.serve()
        except (ModuleError, dagger.QueryError) as e:
            await record_exception(e)
            return 2
        except Exception as e:
            logger.exception("Unhandled exception")
            await record_exception(e)
            return 1


def load_module() -> Module:
    """Load the dagger.Module instance via the main object entry point.

    DEPRECATED: Use load_module_for_registration() or load_module_for_invocation().
    This function is kept for backwards compatibility.
    """
    return load_module_for_invocation()


def load_module_for_registration() -> Module:
    """Load module via AST analysis for registration (no dependencies needed)."""
    from dagger.mod._ast_analyzer import ModuleAnalyzer
    from dagger.mod._ast_types import ASTModuleInfo

    source_paths = get_module_source_paths()

    logger.debug("Analyzing module source files via AST: %s", source_paths)

    try:
        analyzer = ModuleAnalyzer()

        # Analyze all source files and merge results
        combined_info = ASTModuleInfo()
        for source_path in source_paths:
            file_info = analyzer.analyze_file(source_path)

            # Merge objects, enums, and module doc
            combined_info.objects.extend(file_info.objects)
            combined_info.enums.extend(file_info.enums)
            if file_info.module_doc and not combined_info.module_doc:
                combined_info.module_doc = file_info.module_doc
            if file_info.source_path and not combined_info.source_path:
                combined_info.source_path = file_info.source_path

    except SyntaxError as e:
        msg = f"Syntax error in module source: {e}"
        raise ModuleLoadError(msg) from e
    except Exception as e:
        logger.exception("Error while analyzing module source via AST")
        raise ModuleLoadError(str(e)) from e

    mod = Module(main_name=MAIN_OBJECT)
    mod._register_from_ast(combined_info)

    return mod


def load_module_for_invocation() -> Module:
    """Load module at runtime for invocation (existing behavior)."""
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


def get_module_source_paths() -> list[Path]:
    """Find the Python source files to analyze.

    Returns a list of paths to analyze, which may be multiple files
    if the module is a package.
    """
    ep = get_entry_point()

    # ep.value is like "main:Main" or "my_module.submodule:MyClass"
    module_name = ep.value.split(":")[0]

    # Find the module's source file
    spec = importlib.util.find_spec(module_name)
    if spec is None or spec.origin is None:
        msg = f"Cannot find source for module: {module_name}"
        raise ModuleLoadError(msg)

    origin = Path(spec.origin)

    # If this is a package (origin is __init__.py), analyze all .py files
    if origin.name == "__init__.py":
        package_dir = origin.parent
        # Collect all .py files in the package (non-recursive for now)
        py_files = list(package_dir.glob("*.py"))
        if py_files:
            return py_files

    return [origin]


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
