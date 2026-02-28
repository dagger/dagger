"""AST-based type analysis for Dagger modules.

This module provides static analysis of Python source code to extract
type information without importing the module. This is critical when
dependencies may not be available at analysis time.

Usage:
    from dagger.mod._analyzer import analyze_module

    metadata = analyze_module(
        source_files=["src/mymodule/__init__.py"],
        main_object_name="MyModule",
    )
"""

from dagger.mod._analyzer.analyze import analyze_module
from dagger.mod._analyzer.metadata import (
    EnumMemberMetadata,
    EnumTypeMetadata,
    FieldMetadata,
    FunctionMetadata,
    LocationMetadata,
    ModuleMetadata,
    ObjectTypeMetadata,
    ParameterMetadata,
    ResolvedType,
)

__all__ = [
    "EnumMemberMetadata",
    "EnumTypeMetadata",
    "FieldMetadata",
    "FunctionMetadata",
    "LocationMetadata",
    "ModuleMetadata",
    "ObjectTypeMetadata",
    "ParameterMetadata",
    "ResolvedType",
    "analyze_module",
]
