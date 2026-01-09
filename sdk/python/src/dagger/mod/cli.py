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
from dagger import telemetry
from dagger.mod._exceptions import ModuleError, ModuleLoadError, record_exception
from dagger.mod._module import MAIN_OBJECT, Module

logger = logging.getLogger(__package__)

ENTRY_POINT_NAME: typing.Final[str] = "main_object"
ENTRY_POINT_GROUP: typing.Final[str] = typing.cast(str, __package__)
IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")


def app(
    mod: Module | None = None,
    register: bool = False,
    analyze: bool = False,
) -> int | None:
    """Entrypoint for a Python Dagger module.

    Args:
        mod: Pre-loaded Module instance (optional)
        register: If True, register types and exit
        analyze: If True, perform AST analysis without loading module
    """
    if analyze:
        return analyze_standalone()

    telemetry.initialize()
    try:
        return anyio.run(main, mod, register)
    finally:
        telemetry.shutdown()


def analyze_standalone() -> int:
    """Perform standalone AST analysis without loading the module.

    This analyzes Python source files using AST parsing to extract
    Dagger type definitions without executing any user code.

    Environment variables
    ---------------------
        DAGGER_SOURCE_DIR: Directory to analyze (default: current directory)
        DAGGER_MAIN_OBJECT: Name of the main object class
        DAGGER_MODULE: Module name (converted to PascalCase for main object)

    Returns
    -------
        0 on success, non-zero on error
    """
    from dagger.mod._analyzer import analyze_package

    try:
        module_ir = _run_analysis(analyze_package)
    except ModuleLoadError as e:
        logger.warning("Module load error: %s", e)
        return 2
    except SyntaxError as e:
        logger.warning("Syntax error: %s", e)
        return 2
    except Exception:
        logger.exception("Unhandled exception during analysis")
        return 1
    else:
        _log_analysis_summary(module_ir)
        return 0


def _run_analysis(analyze_package):
    """Run the package analysis."""
    source_dir = Path(os.getenv("DAGGER_SOURCE_DIR", "."))
    main_object = os.getenv("DAGGER_MAIN_OBJECT")
    return analyze_package(source_dir, main_object)


def _log_analysis_summary(module_ir) -> None:
    """Log a summary of the analyzed module."""
    logger.info("Analyzed module: %s", module_ir.main_object_name)
    logger.info("  Objects: %d", len(module_ir.get_object_types()))
    logger.info("  Interfaces: %d", len(module_ir.get_interfaces()))
    logger.info("  Enums: %d", len(module_ir.get_enums()))

    for obj in module_ir.objects:
        kind = _get_object_kind(obj)
        logger.info("  - %s (%s)", obj.name, kind)
        _log_functions(obj)
        _log_fields(obj)
        _log_enum_members(obj)


def _get_object_kind(obj) -> str:
    """Get the kind of object (enum, interface, or object)."""
    if obj.is_enum:
        return "enum"
    if obj.is_interface:
        return "interface"
    return "object"


def _log_functions(obj) -> None:
    """Log function signatures for an object."""
    for func in obj.functions:
        params = ", ".join(p.api_name for p in func.parameters)
        ret = func.return_annotation.raw if func.return_annotation else "None"
        logger.info("      %s(%s) -> %s", func.api_name, params, ret)


def _log_fields(obj) -> None:
    """Log fields for an object."""
    for field in obj.fields:
        logger.info("      %s: %s", field.api_name, field.annotation.raw)


def _log_enum_members(obj) -> None:
    """Log enum members for an object."""
    for member in obj.enum_members:
        logger.info("      %s = %s", member.name, member.value)


async def analyze_and_register() -> dagger.ModuleID:
    """Analyze module using AST and register types with Dagger.

    This is the new registration path that uses AST parsing instead of
    runtime introspection.
    """
    from dagger.mod._analyzer import analyze_package
    from dagger.mod._ast_resolver import IRToTypeDefConverter

    # Get source directory
    source_dir = Path(os.getenv("DAGGER_SOURCE_DIR", "."))
    main_object = os.getenv("DAGGER_MAIN_OBJECT")

    # Analyze module using AST
    module_ir = analyze_package(source_dir, main_object)

    # Convert IR to TypeDefs
    converter = IRToTypeDefConverter(module_ir)
    return await converter.convert()


async def main(mod: Module | None = None, register: bool = False) -> int | None:
    """Async entrypoint for a Dagger module."""
    # Establishing connection early on to allow returning dag.error().
    # Note: if there's a connection error dag.error() won't be sent but
    # should be logged and the traceback shown on the function's stderr output.
    async with await dagger.connect():
        try:
            if mod is None:
                mod = load_module()
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
