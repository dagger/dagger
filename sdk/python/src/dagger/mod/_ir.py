"""Intermediate representation for AST-based module analysis.

This module defines immutable dataclasses representing parsed Python module
definitions. The IR serves as a contract between AST parsing and TypeDef
generation, enabling static analysis without code execution.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


@dataclass(frozen=True, slots=True)
class SourceLocation:
    """Source code location for error reporting."""

    file: Path
    line: int
    column: int

    def __str__(self) -> str:
        return f"{self.file}:{self.line}:{self.column}"


@dataclass(frozen=True, slots=True)
class TypeAnnotation:
    """Parsed type annotation from AST.

    Represents a Python type annotation with extracted metadata from
    Annotated[] types and optionality information.
    """

    raw: str  # Original annotation string (from ast.unparse)
    location: SourceLocation | None = None

    # Parsed type structure
    is_optional: bool = False
    is_list: bool = False
    element_type: str | None = None  # For list[T], the T

    # Metadata extracted from Annotated[]
    doc: str | None = None
    name: str | None = None  # From Name() - API name override
    default_path: str | None = None  # From DefaultPath()
    ignore: tuple[str, ...] | None = None  # From Ignore()
    deprecated: str | None = None  # From Deprecated()


@dataclass(frozen=True, slots=True)
class ParameterIR:
    """Function parameter intermediate representation."""

    python_name: str  # Original Python parameter name
    api_name: str  # Converted API name (camelCase)
    annotation: TypeAnnotation
    location: SourceLocation | None = None

    has_default: bool = False
    default_value: Any = None  # Actual default value if simple constant
    default_repr: str | None = None  # String representation for complex defaults


@dataclass(frozen=True, slots=True)
class FunctionIR:
    """Function/method intermediate representation."""

    python_name: str  # Original Python method name
    api_name: str  # Converted API name (camelCase, or "" for constructor)
    parameters: tuple[ParameterIR, ...]
    return_annotation: TypeAnnotation | None
    location: SourceLocation

    # Documentation
    doc: str | None = None

    # Decorator metadata
    is_constructor: bool = False
    is_check: bool = False
    cache_policy: str | None = None  # "never", "session", or TTL duration
    deprecated: str | None = None

    # Method type
    is_async: bool = False


@dataclass(frozen=True, slots=True)
class FieldIR:
    """Field intermediate representation."""

    python_name: str  # Original Python field name
    api_name: str  # Converted API name (camelCase)
    annotation: TypeAnnotation
    location: SourceLocation

    has_default: bool = False
    default_value: Any = None
    default_repr: str | None = None
    init: bool = True  # Whether included in constructor
    deprecated: str | None = None


@dataclass(frozen=True, slots=True)
class EnumMemberIR:
    """Enum member intermediate representation."""

    name: str  # Member name (UPPER_CASE typically)
    value: str  # String representation of value
    doc: str | None = None
    deprecated: str | None = None
    location: SourceLocation | None = None


@dataclass(frozen=True, slots=True)
class ObjectTypeIR:
    """Object type intermediate representation.

    Represents a class decorated with @object_type, @interface, or @enum_type.
    """

    name: str  # Class name (PascalCase)
    qualified_name: str  # module.path.ClassName
    location: SourceLocation
    module_path: Path

    # Type classification
    is_interface: bool = False
    is_enum: bool = False

    # Members (mutually exclusive based on type)
    fields: tuple[FieldIR, ...] = field(default_factory=tuple)
    functions: tuple[FunctionIR, ...] = field(default_factory=tuple)
    enum_members: tuple[EnumMemberIR, ...] = field(default_factory=tuple)

    # Metadata
    doc: str | None = None
    deprecated: str | None = None

    def get_constructor(self) -> FunctionIR | None:
        """Get the constructor function if defined."""
        for func in self.functions:
            if func.is_constructor:
                return func
        return None


@dataclass(frozen=True, slots=True)
class ModuleIR:
    """Complete module intermediate representation.

    Contains all parsed types from a Dagger Python module package.
    """

    main_object_name: str
    objects: tuple[ObjectTypeIR, ...]
    source_files: tuple[Path, ...] = field(default_factory=tuple)
    module_doc: str | None = None

    def get_main_object(self) -> ObjectTypeIR | None:
        """Get the main object type."""
        for obj in self.objects:
            if obj.name == self.main_object_name:
                return obj
        return None

    def get_object(self, name: str) -> ObjectTypeIR | None:
        """Get an object type by name."""
        for obj in self.objects:
            if obj.name == name:
                return obj
        return None

    def get_enums(self) -> tuple[ObjectTypeIR, ...]:
        """Get all enum types."""
        return tuple(obj for obj in self.objects if obj.is_enum)

    def get_interfaces(self) -> tuple[ObjectTypeIR, ...]:
        """Get all interface types."""
        return tuple(obj for obj in self.objects if obj.is_interface)

    def get_object_types(self) -> tuple[ObjectTypeIR, ...]:
        """Get all object types (non-enum, non-interface)."""
        return tuple(
            obj for obj in self.objects if not obj.is_enum and not obj.is_interface
        )
