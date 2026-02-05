"""Metadata dataclasses for AST analysis results.

These classes represent the structured output of AST analysis,
serving as the single source of truth for type information.
"""

from __future__ import annotations

import dataclasses
import json
from typing import Any


@dataclasses.dataclass(slots=True, frozen=True)
class LocationMetadata:
    """Source location for error messages and debugging."""

    file: str
    line: int
    column: int

    def __str__(self) -> str:
        return f"{self.file}:{self.line}:{self.column}"


@dataclasses.dataclass(slots=True)
class ResolvedType:
    """Resolved type information.

    This represents a fully resolved type that can be converted to a TypeDef.
    """

    kind: str  # "primitive", "list", "object", "enum", "interface", "scalar", "void"
    name: str  # Type name (e.g., "str", "Container", "MyObject")
    is_optional: bool = False  # Whether type is T | None
    element_type: ResolvedType | None = None  # For list types
    is_self: bool = False  # Whether this was typing.Self

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        result: dict[str, Any] = {
            "kind": self.kind,
            "name": self.name,
            "is_optional": self.is_optional,
            "is_self": self.is_self,
        }
        if self.element_type is not None:
            result["element_type"] = self.element_type.to_dict()
        return result

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> ResolvedType:
        """Create from dictionary."""
        element_type = None
        if data.get("element_type"):
            element_type = cls.from_dict(data["element_type"])
        return cls(
            kind=data["kind"],
            name=data["name"],
            is_optional=data.get("is_optional", False),
            element_type=element_type,
            is_self=data.get("is_self", False),
        )


@dataclasses.dataclass(slots=True)
class ParameterMetadata:
    """Metadata for a function parameter extracted from AST."""

    # Names
    python_name: str  # Original Python parameter name
    api_name: str  # API name (after normalization, e.g., from_ -> from)

    # Type information
    type_annotation: str  # String representation of the annotation
    resolved_type: ResolvedType  # Resolved type structure

    # Optional/default handling
    is_nullable: bool  # Whether type is T | None
    has_default: bool  # Whether parameter has a default value
    default_value: Any | None = None  # The default value if extractable

    # Metadata from Annotated[T, ...]
    doc: str | None = None  # From Doc()
    ignore: list[str] | None = None  # From Ignore()
    default_path: str | None = None  # From DefaultPath()
    default_address: str | None = None  # From DefaultAddress()
    deprecated: str | None = None  # From Deprecated()
    alt_name: str | None = None  # From Name() - used to set api_name

    # Source location
    location: LocationMetadata | None = None

    @property
    def is_optional(self) -> bool:
        """Whether this parameter is optional in the API."""
        return any(
            [
                self.has_default,
                self.default_path is not None,
                self.default_address is not None,
                self.is_nullable,
            ]
        )

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "python_name": self.python_name,
            "api_name": self.api_name,
            "type_annotation": self.type_annotation,
            "resolved_type": self.resolved_type.to_dict(),
            "is_nullable": self.is_nullable,
            "has_default": self.has_default,
            "default_value": self.default_value,
            "doc": self.doc,
            "ignore": self.ignore,
            "default_path": self.default_path,
            "default_address": self.default_address,
            "deprecated": self.deprecated,
            "alt_name": self.alt_name,
            "location": dataclasses.asdict(self.location) if self.location else None,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> ParameterMetadata:
        """Create from dictionary."""
        location = None
        if data.get("location"):
            location = LocationMetadata(**data["location"])
        return cls(
            python_name=data["python_name"],
            api_name=data["api_name"],
            type_annotation=data["type_annotation"],
            resolved_type=ResolvedType.from_dict(data["resolved_type"]),
            is_nullable=data["is_nullable"],
            has_default=data["has_default"],
            default_value=data.get("default_value"),
            doc=data.get("doc"),
            ignore=data.get("ignore"),
            default_path=data.get("default_path"),
            default_address=data.get("default_address"),
            deprecated=data.get("deprecated"),
            alt_name=data.get("alt_name"),
            location=location,
        )


@dataclasses.dataclass(slots=True)
class FunctionMetadata:
    """Metadata for a @function decorated method."""

    # Names
    python_name: str  # Original method name
    api_name: str  # API name (after normalization)

    # Return type
    return_type_annotation: str  # String representation
    resolved_return_type: ResolvedType  # Resolved type structure

    # Parameters (excluding self)
    parameters: list[ParameterMetadata] = dataclasses.field(default_factory=list)

    # Decorator metadata
    doc: str | None = None
    deprecated: str | None = None
    cache_policy: str | None = None
    is_check: bool = False

    # Function characteristics
    is_async: bool = False
    is_classmethod: bool = False
    is_constructor: bool = False  # True for __init__ or create() classmethod

    # Source location
    location: LocationMetadata | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "python_name": self.python_name,
            "api_name": self.api_name,
            "return_type_annotation": self.return_type_annotation,
            "resolved_return_type": self.resolved_return_type.to_dict(),
            "parameters": [p.to_dict() for p in self.parameters],
            "doc": self.doc,
            "deprecated": self.deprecated,
            "cache_policy": self.cache_policy,
            "is_check": self.is_check,
            "is_async": self.is_async,
            "is_classmethod": self.is_classmethod,
            "is_constructor": self.is_constructor,
            "location": dataclasses.asdict(self.location) if self.location else None,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> FunctionMetadata:
        """Create from dictionary."""
        location = None
        if data.get("location"):
            location = LocationMetadata(**data["location"])
        return cls(
            python_name=data["python_name"],
            api_name=data["api_name"],
            return_type_annotation=data["return_type_annotation"],
            resolved_return_type=ResolvedType.from_dict(data["resolved_return_type"]),
            parameters=[
                ParameterMetadata.from_dict(p) for p in data.get("parameters", [])
            ],
            doc=data.get("doc"),
            deprecated=data.get("deprecated"),
            cache_policy=data.get("cache_policy"),
            is_check=data.get("is_check", False),
            is_async=data.get("is_async", False),
            is_classmethod=data.get("is_classmethod", False),
            is_constructor=data.get("is_constructor", False),
            location=location,
        )


@dataclasses.dataclass(slots=True)
class FieldMetadata:
    """Metadata for a field() declared attribute."""

    # Names
    python_name: str  # Original field name
    api_name: str  # API name (after normalization)

    # Type information
    type_annotation: str  # String representation
    resolved_type: ResolvedType  # Resolved type structure

    # Field options
    has_default: bool
    default_value: Any | None = None
    deprecated: str | None = None
    init: bool = True  # Whether field is in constructor
    doc: str | None = None

    # Source location
    location: LocationMetadata | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "python_name": self.python_name,
            "api_name": self.api_name,
            "type_annotation": self.type_annotation,
            "resolved_type": self.resolved_type.to_dict(),
            "has_default": self.has_default,
            "default_value": self.default_value,
            "deprecated": self.deprecated,
            "init": self.init,
            "doc": self.doc,
            "location": dataclasses.asdict(self.location) if self.location else None,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> FieldMetadata:
        """Create from dictionary."""
        location = None
        if data.get("location"):
            location = LocationMetadata(**data["location"])
        return cls(
            python_name=data["python_name"],
            api_name=data["api_name"],
            type_annotation=data["type_annotation"],
            resolved_type=ResolvedType.from_dict(data["resolved_type"]),
            has_default=data["has_default"],
            default_value=data.get("default_value"),
            deprecated=data.get("deprecated"),
            init=data.get("init", True),
            doc=data.get("doc"),
            location=location,
        )


@dataclasses.dataclass(slots=True)
class ObjectTypeMetadata:
    """Metadata for an @object_type or @interface decorated class."""

    # Name
    name: str  # Class name

    # Type flags
    is_interface: bool = False

    # Class metadata
    doc: str | None = None
    deprecated: str | None = None

    # Members
    fields: list[FieldMetadata] = dataclasses.field(default_factory=list)
    functions: list[FunctionMetadata] = dataclasses.field(default_factory=list)
    constructor: FunctionMetadata | None = None

    # Source location
    location: LocationMetadata | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "name": self.name,
            "is_interface": self.is_interface,
            "doc": self.doc,
            "deprecated": self.deprecated,
            "fields": [f.to_dict() for f in self.fields],
            "functions": [f.to_dict() for f in self.functions],
            "constructor": self.constructor.to_dict() if self.constructor else None,
            "location": dataclasses.asdict(self.location) if self.location else None,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> ObjectTypeMetadata:
        """Create from dictionary."""
        location = None
        if data.get("location"):
            location = LocationMetadata(**data["location"])
        constructor = None
        if data.get("constructor"):
            constructor = FunctionMetadata.from_dict(data["constructor"])
        return cls(
            name=data["name"],
            is_interface=data.get("is_interface", False),
            doc=data.get("doc"),
            deprecated=data.get("deprecated"),
            fields=[FieldMetadata.from_dict(f) for f in data.get("fields", [])],
            functions=[
                FunctionMetadata.from_dict(f) for f in data.get("functions", [])
            ],
            constructor=constructor,
            location=location,
        )


@dataclasses.dataclass(slots=True)
class EnumMemberMetadata:
    """Metadata for an enum member."""

    name: str
    value: str
    doc: str | None = None
    deprecated: str | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "name": self.name,
            "value": self.value,
            "doc": self.doc,
            "deprecated": self.deprecated,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> EnumMemberMetadata:
        """Create from dictionary."""
        return cls(
            name=data["name"],
            value=data["value"],
            doc=data.get("doc"),
            deprecated=data.get("deprecated"),
        )


@dataclasses.dataclass(slots=True)
class EnumTypeMetadata:
    """Metadata for an @enum_type decorated enum."""

    name: str
    doc: str | None = None
    members: list[EnumMemberMetadata] = dataclasses.field(default_factory=list)
    location: LocationMetadata | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization."""
        return {
            "name": self.name,
            "doc": self.doc,
            "members": [m.to_dict() for m in self.members],
            "location": dataclasses.asdict(self.location) if self.location else None,
        }

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> EnumTypeMetadata:
        """Create from dictionary."""
        location = None
        if data.get("location"):
            location = LocationMetadata(**data["location"])
        return cls(
            name=data["name"],
            doc=data.get("doc"),
            members=[EnumMemberMetadata.from_dict(m) for m in data.get("members", [])],
            location=location,
        )


@dataclasses.dataclass(slots=True)
class ModuleMetadata:
    """Complete module metadata - single source of truth.

    This is the root structure returned by analyze_module() and contains
    all type information needed for registration.
    """

    # Module identification
    module_name: str
    main_object: str

    # Module-level documentation
    doc: str | None = None

    # Type definitions
    objects: dict[str, ObjectTypeMetadata] = dataclasses.field(default_factory=dict)
    enums: dict[str, EnumTypeMetadata] = dataclasses.field(default_factory=dict)

    def to_json(self) -> str:
        """Serialize to JSON for file storage."""
        return json.dumps(self.to_dict(), indent=2)

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary."""
        return {
            "module_name": self.module_name,
            "main_object": self.main_object,
            "doc": self.doc,
            "objects": {k: v.to_dict() for k, v in self.objects.items()},
            "enums": {k: v.to_dict() for k, v in self.enums.items()},
        }

    @classmethod
    def from_json(cls, data: str) -> ModuleMetadata:
        """Deserialize from JSON."""
        return cls.from_dict(json.loads(data))

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> ModuleMetadata:
        """Create from dictionary."""
        objects = {
            k: ObjectTypeMetadata.from_dict(v)
            for k, v in data.get("objects", {}).items()
        }
        enums = {
            k: EnumTypeMetadata.from_dict(v) for k, v in data.get("enums", {}).items()
        }
        return cls(
            module_name=data["module_name"],
            main_object=data["main_object"],
            doc=data.get("doc"),
            objects=objects,
            enums=enums,
        )
