"""Data structures for AST-extracted module information."""

from __future__ import annotations

import dataclasses
from pathlib import Path
from typing import Any


@dataclasses.dataclass(slots=True)
class ASTTypeAnnotation:
    """Type annotation extracted from AST."""

    raw: str  # Original annotation string representation
    base_type: str  # e.g., "str", "Container", "list"
    type_args: list[ASTTypeAnnotation] = dataclasses.field(default_factory=list)
    is_optional: bool = False
    # Metadata from Annotated[T, ...] - list of (kind, value) tuples
    # kind: "doc", "name", "default_path", "ignore", "deprecated"
    annotated_metadata: list[tuple[str, Any]] = dataclasses.field(default_factory=list)


@dataclasses.dataclass(slots=True)
class ASTParameter:
    """Parameter extracted from function signature AST."""

    name: str  # Original Python name
    annotation: ASTTypeAnnotation | None = None
    default_value: Any | None = None
    has_default: bool = False
    # Metadata extracted from Annotated
    alt_name: str | None = None  # from Name()
    doc: str | None = None  # from Doc()
    default_path: str | None = None  # from DefaultPath()
    ignore: list[str] | None = None  # from Ignore()
    deprecated: str | None = None  # from Deprecated()


@dataclasses.dataclass(slots=True)
class ASTFunctionDef:
    """Function definition extracted from AST."""

    name: str  # API name (camelCase)
    original_name: str  # Original Python name
    parameters: list[ASTParameter] = dataclasses.field(default_factory=list)
    return_annotation: ASTTypeAnnotation | None = None
    docstring: str | None = None
    is_async: bool = False
    # Decorator metadata
    alt_name: str | None = None  # from @function(name="...")
    alt_doc: str | None = None  # from @function(doc="...")
    cache_policy: str | None = None  # from @function(cache="...")
    deprecated: str | None = None  # from @function(deprecated="...")
    is_check: bool = False  # from @check decorator
    is_constructor: bool = False  # True if this is an __init__ or @function(cls)
    line_number: int = 0

    @property
    def api_name(self) -> str:
        """Return the name to use in the API."""
        return self.alt_name or self.name


@dataclasses.dataclass(slots=True)
class ASTFieldDef:
    """Field definition extracted from AST."""

    name: str  # API name (camelCase)
    original_name: str  # Original Python name
    annotation: ASTTypeAnnotation | None = None
    has_default: bool = False
    default_value: Any | None = None
    deprecated: str | None = None
    line_number: int = 0

    @property
    def api_name(self) -> str:
        """Return the name to use in the API."""
        return self.name


@dataclasses.dataclass(slots=True)
class ASTEnumMember:
    """Enum member extracted from AST."""

    name: str
    value: str
    docstring: str | None = None
    deprecated: str | None = None
    line_number: int = 0


@dataclasses.dataclass(slots=True)
class ASTEnumDef:
    """Enum definition extracted from AST."""

    name: str
    docstring: str | None = None
    members: list[ASTEnumMember] = dataclasses.field(default_factory=list)
    line_number: int = 0


@dataclasses.dataclass(slots=True)
class ASTObjectDef:
    """Object type definition extracted from AST."""

    name: str
    docstring: str | None = None
    fields: list[ASTFieldDef] = dataclasses.field(default_factory=list)
    functions: list[ASTFunctionDef] = dataclasses.field(default_factory=list)
    constructor: ASTFunctionDef | None = None
    deprecated: str | None = None
    is_interface: bool = False
    line_number: int = 0


@dataclasses.dataclass(slots=True)
class ASTModuleInfo:
    """Complete module information extracted from AST."""

    objects: list[ASTObjectDef] = dataclasses.field(default_factory=list)
    enums: list[ASTEnumDef] = dataclasses.field(default_factory=list)
    source_path: Path | None = None
    module_doc: str | None = None  # Module-level docstring
