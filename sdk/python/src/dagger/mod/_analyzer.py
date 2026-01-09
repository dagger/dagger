"""Python module analyzer using AST parsing.

This module provides the main entry point for analyzing Dagger Python modules
without executing the code. It parses Python source files, extracts decorated
types, and builds an intermediate representation.
"""

from __future__ import annotations

import os
from collections.abc import Sequence
from pathlib import Path

from dagger.mod._ast_visitor import get_module_docstring, parse_file
from dagger.mod._exceptions import ModuleLoadError
from dagger.mod._ir import ModuleIR, ObjectTypeIR
from dagger.mod._utils import to_pascal_case


class PythonModuleAnalyzer:
    """Analyzes Python modules for Dagger types using AST parsing.

    This analyzer parses Python source files without executing them,
    extracting decorated classes (@object_type, @function, etc.) and
    building an intermediate representation (IR) that can be converted
    to Dagger TypeDefs.
    """

    def __init__(
        self,
        source_paths: Sequence[Path],
        main_object_name: str | None = None,
    ):
        """Initialize the analyzer.

        Args:
            source_paths: Python files to analyze
            main_object_name: Expected main object name (PascalCase).
                If not provided, will be inferred from environment or
                module structure.
        """
        self.source_paths = list(source_paths)
        self.main_object_name = main_object_name

    def analyze(self) -> ModuleIR:
        """Analyze all source files and build module IR.

        Returns
        -------
            ModuleIR containing all discovered types

        Raises
        ------
            ModuleLoadError: If main object not found or analysis fails
            SyntaxError: If source files contain invalid Python syntax
        """
        all_objects: list[ObjectTypeIR] = []
        errors: list[str] = []

        for source_path in self.source_paths:
            try:
                objects = parse_file(source_path)
                all_objects.extend(objects)
            except SyntaxError as e:
                errors.append(f"Syntax error in {source_path}: {e}")
            except OSError as e:
                errors.append(f"Failed to read {source_path}: {e}")

        if errors:
            msg = "Failed to analyze module:\n" + "\n".join(errors)
            raise ModuleLoadError(msg)

        # Infer main object if not provided
        if self.main_object_name is None:
            self.main_object_name = self._infer_main_object(all_objects)

        # Validate main object exists
        main_obj = next(
            (obj for obj in all_objects if obj.name == self.main_object_name),
            None,
        )
        if main_obj is None:
            found_names = [obj.name for obj in all_objects]
            msg = (
                f"Main object '{self.main_object_name}' not found. "
                f"Found objects: {found_names}"
            )
            raise ModuleLoadError(msg)

        # Extract module-level docstring from first source file
        module_doc = None
        if self.source_paths:
            module_doc = get_module_docstring(self.source_paths[0])

        return ModuleIR(
            main_object_name=self.main_object_name,
            objects=tuple(all_objects),
            source_files=tuple(self.source_paths),
            module_doc=module_doc,
        )

    def _infer_main_object(self, objects: list[ObjectTypeIR]) -> str:
        """Infer main object name from environment or convention.

        Priority:
        1. DAGGER_MAIN_OBJECT environment variable
        2. DAGGER_MODULE environment variable (converted to PascalCase)
        3. Object named "Main"
        4. Single object in module

        Raises
        ------
            ModuleLoadError: If main object cannot be determined
        """
        # Check DAGGER_MAIN_OBJECT first
        if env_name := os.getenv("DAGGER_MAIN_OBJECT"):
            return env_name

        # Check DAGGER_MODULE
        if module_name := os.getenv("DAGGER_MODULE"):
            return to_pascal_case(module_name)

        # Check for "Main" convention
        if any(obj.name == "Main" for obj in objects):
            return "Main"

        # If only one object, use it
        if len(objects) == 1:
            return objects[0].name

        # Can't determine
        found_names = [obj.name for obj in objects]
        msg = (
            "Could not determine main object. "
            "Set DAGGER_MAIN_OBJECT or DAGGER_MODULE environment variable. "
            f"Found objects: {found_names}"
        )
        raise ModuleLoadError(msg)


def analyze_package(
    package_path: Path,
    main_object_name: str | None = None,
    *,
    recursive: bool = True,
) -> ModuleIR:
    """Analyze a Python package directory for Dagger types.

    Args:
        package_path: Directory containing Python files
        main_object_name: Expected main object name
        recursive: Whether to search subdirectories

    Returns
    -------
        ModuleIR with all discovered types

    Raises
    ------
        ModuleLoadError: If no Python files found or main object not found
    """
    if not package_path.is_dir():
        # Single file
        if package_path.suffix == ".py":
            return PythonModuleAnalyzer([package_path], main_object_name).analyze()
        msg = f"Not a Python file or directory: {package_path}"
        raise ModuleLoadError(msg)

    # Find Python files
    pattern = "**/*.py" if recursive else "*.py"
    python_files = sorted(package_path.glob(pattern))

    # Filter out __pycache__ and hidden directories
    python_files = [
        f
        for f in python_files
        if "__pycache__" not in f.parts and not any(p.startswith(".") for p in f.parts)
    ]

    if not python_files:
        msg = f"No Python files found in {package_path}"
        raise ModuleLoadError(msg)

    return PythonModuleAnalyzer(python_files, main_object_name).analyze()


def analyze_source(
    source_code: str,
    filename: str = "<string>",
    main_object_name: str | None = None,
) -> ModuleIR:
    """Analyze Python source code string for Dagger types.

    Useful for testing and dynamic analysis.

    Args:
        source_code: Python source code
        filename: Virtual filename for error messages
        main_object_name: Expected main object name

    Returns
    -------
        ModuleIR with all discovered types
    """
    import ast

    from dagger.mod._ast_visitor import DaggerModuleVisitor

    # Parse source
    tree = ast.parse(source_code, filename=filename)

    # Create a temporary path for the visitor
    temp_path = Path(filename)

    visitor = DaggerModuleVisitor(temp_path, source_code)
    visitor.visit(tree)

    objects = visitor.objects

    # Infer main object
    if main_object_name is None:
        if len(objects) == 1:
            main_object_name = objects[0].name
        elif any(obj.name == "Main" for obj in objects):
            main_object_name = "Main"
        else:
            found_names = [obj.name for obj in objects]
            msg = f"Could not determine main object. Found: {found_names}"
            raise ModuleLoadError(msg)
    # Validate explicitly provided main object exists
    elif not any(obj.name == main_object_name for obj in objects):
        found_names = [obj.name for obj in objects]
        msg = f"Main object '{main_object_name}' not found. Found: {found_names}"
        raise ModuleLoadError(msg)

    # Extract module docstring
    module_doc = ast.get_docstring(tree)

    return ModuleIR(
        main_object_name=main_object_name,
        objects=tuple(objects),
        source_files=(temp_path,),
        module_doc=module_doc,
    )
