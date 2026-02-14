"""Main entry point for AST-based module analysis.

This module provides the analyze_module() function that performs
static analysis of Python source files to extract type information
without importing the module.
"""

from __future__ import annotations

import ast
import logging
from pathlib import Path

from dagger.mod._analyzer.errors import AnalysisError, ValidationError
from dagger.mod._analyzer.metadata import ModuleMetadata
from dagger.mod._analyzer.parser import ModuleParser

logger = logging.getLogger(__name__)


def analyze_module(
    source_files: list[str | Path],
    main_object_name: str,
    *,
    module_name: str | None = None,
) -> ModuleMetadata:
    """Analyze Python source files and extract module metadata.

    This is the main entry point for AST-based type analysis. It parses
    the source files, extracts decorated classes and functions, resolves
    type annotations, and returns structured metadata.
    """
    if not source_files:
        msg = "No source files provided"
        raise AnalysisError(msg)

    # Convert to paths
    paths = [Path(f) for f in source_files]

    # Validate files exist
    for path in paths:
        if not path.exists():
            msg = f"Source file not found: {path}"
            raise AnalysisError(msg)
        if not path.is_file():
            msg = f"Not a file: {path}"
            raise AnalysisError(msg)

    logger.debug(
        "Analyzing module: main_object=%s, files=%s",
        main_object_name,
        [str(p) for p in paths],
    )

    # Parse all files
    parser = ModuleParser(
        source_files=paths,
        main_object_name=main_object_name,
    )

    objects, enums = parser.parse()

    # Validate main object exists
    if main_object_name not in objects:
        available = list(objects.keys())
        msg = (
            f"Main object '{main_object_name}' not found. "
            f"Available objects: {available}. "
            "Ensure the main class is decorated with @dagger.object_type."
        )
        raise ValidationError(msg)

    # Extract module documentation from main object's module
    module_doc = _extract_module_doc(paths)

    # Build metadata
    metadata = ModuleMetadata(
        module_name=module_name or main_object_name,
        main_object=main_object_name,
        doc=module_doc,
        objects=objects,
        enums=enums,
    )

    logger.debug(
        "Analysis complete: objects=%d, enums=%d",
        len(objects),
        len(enums),
    )

    return metadata


def _extract_module_doc(source_files: list[Path]) -> str | None:
    """Extract module-level docstring from the first source file."""
    # Prefer __init__.py
    init_files = [f for f in source_files if f.name == "__init__.py"]
    if init_files:
        target = init_files[0]
    elif source_files:
        target = source_files[0]
    else:
        return None

    try:
        source = target.read_text(encoding="utf-8")
        tree = ast.parse(source)
        return ast.get_docstring(tree)
    except (OSError, SyntaxError):
        return None


def analyze_source_string(
    source: str,
    main_object_name: str,
    *,
    module_name: str | None = None,
) -> ModuleMetadata:
    """Analyze Python source code from a string.

    This is useful for testing or when source is not in a file.
    """
    import tempfile

    # Write to a temporary file
    with tempfile.NamedTemporaryFile(
        mode="w",
        suffix=".py",
        delete=False,
        encoding="utf-8",
    ) as f:
        f.write(source)
        temp_path = f.name

    try:
        return analyze_module(
            source_files=[temp_path],
            main_object_name=main_object_name,
            module_name=module_name,
        )
    finally:
        # Clean up temp file
        Path(temp_path).unlink(missing_ok=True)
