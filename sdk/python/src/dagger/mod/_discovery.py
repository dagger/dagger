"""Module discovery and AST-based registration utilities.

Provides functions to locate Python source files for a Dagger module
and register module types using AST analysis.
"""

from __future__ import annotations

import importlib
import importlib.machinery
import importlib.util
import logging
import os
import typing
from pathlib import Path

import dagger

IMPORT_PKG: typing.Final[str] = os.getenv("DAGGER_DEFAULT_PYTHON_PACKAGE", "main")

logger = logging.getLogger(__name__)


async def ast_register(
    main_object_name: str,
    module_name: str,
) -> dagger.ModuleID:
    """Analyze source files and register module types with the Dagger engine.

    This is the shared pipeline used by both the CLI registration path
    and the Module._typedefs() path.
    """
    from dagger.mod._analyzer import analyze_module
    from dagger.mod._analyzer.registration import register_from_metadata

    source_files = find_source_files()
    if not source_files:
        msg = (
            "No Python source files found for module. "
            f"Looking for package: {IMPORT_PKG}"
        )
        raise RuntimeError(msg)

    logger.debug("Found source files: %s", source_files)

    metadata = analyze_module(
        source_files=source_files,
        main_object_name=main_object_name,
        module_name=module_name,
    )

    logger.debug(
        "Analyzed module: %d objects, %d enums",
        len(metadata.objects),
        len(metadata.enums),
    )

    return await register_from_metadata(metadata)


def find_source_files() -> list[str]:
    """Find Python source files for the module.

    Returns a list of .py files in the module package.
    """
    spec = _find_module_spec()

    if spec is None:
        return []

    # Single-file module
    if spec.submodule_search_locations is None:
        if spec.origin and spec.origin.endswith(".py"):
            return [spec.origin]
        return []

    # Package module — find all .py files
    source_files: list[str] = []
    for location in spec.submodule_search_locations:
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
