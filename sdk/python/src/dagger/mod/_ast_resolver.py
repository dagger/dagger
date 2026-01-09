"""Type resolution from AST IR to Dagger TypeDefs.

This module converts parsed intermediate representations to Dagger API
TypeDef objects using dag.type_def() and related builders.
"""

from __future__ import annotations

import contextlib
import json
from typing import TYPE_CHECKING

import dagger
from dagger import dag
from dagger.mod._ir import (
    FieldIR,
    FunctionIR,
    ModuleIR,
    ObjectTypeIR,
    ParameterIR,
    TypeAnnotation,
)

if TYPE_CHECKING:
    from dagger import TypeDef


# Core Dagger types from gen.py - loaded lazily
_DAGGER_TYPES: set[str] | None = None


def _get_dagger_types() -> set[str]:
    """Load Dagger core types from gen.py."""
    global _DAGGER_TYPES
    if _DAGGER_TYPES is None:
        try:
            from dagger.client import gen

            _DAGGER_TYPES = {
                name
                for name in dir(gen)
                if not name.startswith("_") and name[0].isupper()
            }
        except ImportError:
            _DAGGER_TYPES = set()
    return _DAGGER_TYPES


def _is_dagger_type(type_name: str) -> bool:
    """Check if a type name is a Dagger core type."""
    return type_name in _get_dagger_types()


# Built-in Python type to TypeDefKind mapping
BUILTIN_TYPE_MAP = {
    "str": dagger.TypeDefKind.STRING_KIND,
    "int": dagger.TypeDefKind.INTEGER_KIND,
    "float": dagger.TypeDefKind.FLOAT_KIND,
    "bool": dagger.TypeDefKind.BOOLEAN_KIND,
    "None": dagger.TypeDefKind.VOID_KIND,
    "NoneType": dagger.TypeDefKind.VOID_KIND,
}


def _extract_base_type(raw: str) -> str:
    """Extract the base type name from a complex annotation string.

    Strips away Optional[], Union[], Annotated[], list[], etc. to get
    the core type name.
    """
    raw = raw.strip()

    # Handle Annotated[T, ...]
    if raw.startswith("Annotated["):
        inner = raw[len("Annotated[") : -1]
        parts = _split_type_args(inner)
        if parts:
            raw = parts[0].strip()

    # Handle Optional[T]
    if raw.startswith("Optional["):
        raw = raw[len("Optional[") : -1].strip()

    # Handle Union[T, None]
    if raw.startswith("Union["):
        inner = raw[len("Union[") : -1]
        parts = _split_type_args(inner)
        for part in parts:
            if part.strip() not in ("None", "NoneType"):
                raw = part.strip()
                break

    # Handle T | None
    if " | " in raw:
        parts = raw.split(" | ")
        for part in parts:
            if part.strip() not in ("None", "NoneType"):
                raw = part.strip()
                break

    # Handle list[T], List[T], Sequence[T]
    for prefix in ("list[", "List[", "Sequence[", "tuple[", "Tuple["):
        if raw.startswith(prefix):
            inner = raw[len(prefix) : -1]
            parts = _split_type_args(inner)
            if parts:
                raw = parts[0].strip()
            break

    # Handle module-qualified names (e.g., dagger.Container)
    if "." in raw:
        raw = raw.split(".")[-1]

    return raw


def _split_type_args(args_str: str) -> list[str]:
    """Split comma-separated type arguments, respecting brackets."""
    parts: list[str] = []
    current: list[str] = []
    depth = 0

    for char in args_str:
        if char in "[({":
            depth += 1
        elif char in "])}":
            depth -= 1
        elif char == "," and depth == 0:
            parts.append("".join(current))
            current = []
            continue
        current.append(char)

    if current:
        parts.append("".join(current))

    return parts


class TypeResolver:
    """Resolves TypeAnnotation IR to Dagger TypeDef objects.

    Handles mapping Python types to Dagger types:
    - Built-in types (str, int, bool, float) -> TypeDefKind
    - Dagger core types (Container, Directory) -> with_object()
    - Module-defined types -> with_object(), with_interface(), with_enum()
    - Lists -> with_list_of()
    - Optional -> with_optional()
    """

    def __init__(self, module_ir: ModuleIR):
        self.module_ir = module_ir
        self._object_names = {obj.name for obj in module_ir.objects}

    def resolve(self, annotation: TypeAnnotation) -> TypeDef:
        """Convert TypeAnnotation to Dagger TypeDef."""
        td = dag.type_def()

        # Handle optional
        if annotation.is_optional:
            td = td.with_optional(True)

        # Handle list types
        if annotation.is_list and annotation.element_type:
            element_annotation = TypeAnnotation(raw=annotation.element_type)
            element_td = self.resolve(element_annotation)
            return td.with_list_of(element_td)

        # Extract and resolve base type
        base_type = _extract_base_type(annotation.raw)

        # Check built-in types
        if base_type in BUILTIN_TYPE_MAP:
            return td.with_kind(BUILTIN_TYPE_MAP[base_type])

        # Check for Dagger core types
        if _is_dagger_type(base_type):
            return td.with_object(base_type)

        # Check for module-defined types
        obj = self.module_ir.get_object(base_type)
        if obj is not None:
            if obj.is_enum:
                return td.with_enum(base_type)
            if obj.is_interface:
                return td.with_interface(base_type)
            return td.with_object(base_type)

        # Check if it's a known type name in our module
        if base_type in self._object_names:
            return td.with_object(base_type)

        # Check for Self return type
        if base_type == "Self":
            # Will need to be resolved in context
            return td.with_object("Self")

        # Fallback: assume it's an object type
        # This handles forward references
        return td.with_object(base_type)

    def resolve_return_type(
        self, annotation: TypeAnnotation | None, context_type: str
    ) -> TypeDef:
        """Resolve return type, handling Self references."""
        if annotation is None:
            return dag.type_def().with_kind(dagger.TypeDefKind.VOID_KIND)

        td = self.resolve(annotation)

        # Replace Self with context type
        base_type = _extract_base_type(annotation.raw)
        if base_type == "Self":
            return dag.type_def().with_object(context_type)

        return td


class IRToTypeDefConverter:
    """Converts complete module IR to Dagger TypeDefs for registration."""

    def __init__(self, module_ir: ModuleIR):
        self.module_ir = module_ir
        self.resolver = TypeResolver(module_ir)

    async def convert(self) -> dagger.ModuleID:
        """Convert module IR to Dagger Module and return its ID."""
        mod = dag.module()

        # Set module description
        if self.module_ir.module_doc:
            mod = mod.with_description(self.module_ir.module_doc)

        # Process all object types
        for obj in self.module_ir.get_object_types():
            type_def = self._convert_object_type(obj)
            mod = mod.with_object(type_def)

        # Process interfaces
        for obj in self.module_ir.get_interfaces():
            type_def = self._convert_interface_type(obj)
            mod = mod.with_interface(type_def)

        # Process enums
        for obj in self.module_ir.get_enums():
            type_def = self._convert_enum_type(obj)
            mod = mod.with_enum(type_def)

        return await mod.id()

    def _convert_object_type(self, obj: ObjectTypeIR) -> TypeDef:
        """Convert object type IR to TypeDef."""
        type_def = dag.type_def().with_object(
            obj.name,
            description=obj.doc,
            deprecated=obj.deprecated,
        )

        # Add fields
        for field in obj.fields:
            type_def = self._add_field(type_def, field)

        # Add functions
        for func in obj.functions:
            type_def = self._add_function(type_def, func, obj.name)

        # Add constructor for main object if exists
        if obj.name == self.module_ir.main_object_name and not any(
            f.is_constructor for f in obj.functions
        ):
            # Add default constructor
            type_def = self._add_default_constructor(type_def, obj)

        return type_def

    def _convert_interface_type(self, obj: ObjectTypeIR) -> TypeDef:
        """Convert interface type IR to TypeDef."""
        type_def = dag.type_def().with_interface(
            obj.name,
            description=obj.doc,
        )

        # Add functions
        for func in obj.functions:
            type_def = self._add_function(type_def, func, obj.name)

        return type_def

    def _convert_enum_type(self, obj: ObjectTypeIR) -> TypeDef:
        """Convert enum type IR to TypeDef."""
        type_def = dag.type_def().with_enum(
            obj.name,
            description=obj.doc,
        )

        for member in obj.enum_members:
            type_def = type_def.with_enum_member(
                member.name,
                value=member.value,
                description=member.doc,
                deprecated=member.deprecated,
            )

        return type_def

    def _add_field(self, type_def: TypeDef, field: FieldIR) -> TypeDef:
        """Add a field to a type definition."""
        field_td = self.resolver.resolve(field.annotation)
        return type_def.with_field(
            field.api_name,
            field_td,
            description=field.annotation.doc,
            deprecated=field.deprecated,
        )

    def _add_function(
        self, type_def: TypeDef, func: FunctionIR, context_type: str
    ) -> TypeDef:
        """Add a function to a type definition."""
        # Build return type
        return_td = self.resolver.resolve_return_type(
            func.return_annotation, context_type
        )

        # Create function def
        func_def = dag.function(func.api_name, return_td)

        if func.doc:
            func_def = func_def.with_description(func.doc.strip())

        # Handle cache policy
        if func.cache_policy is not None:
            if func.cache_policy == "never":
                func_def = func_def.with_cache_policy(dagger.FunctionCachePolicy.Never)
            elif func.cache_policy == "session":
                func_def = func_def.with_cache_policy(
                    dagger.FunctionCachePolicy.PerSession
                )
            elif func.cache_policy:
                func_def = func_def.with_cache_policy(
                    dagger.FunctionCachePolicy.Default,
                    time_to_live=func.cache_policy,
                )

        if func.deprecated:
            func_def = func_def.with_deprecated(reason=func.deprecated)

        if func.is_check:
            func_def = func_def.with_check()

        # Add parameters
        for param in func.parameters:
            func_def = self._add_parameter(func_def, param)

        if func.is_constructor or func.api_name == "":
            return type_def.with_constructor(func_def)
        return type_def.with_function(func_def)

    def _add_parameter(
        self, func_def: dagger.Function, param: ParameterIR
    ) -> dagger.Function:
        """Add a parameter to a function definition."""
        param_td = self.resolver.resolve(param.annotation)

        # Handle nullable/optional
        is_nullable = param.annotation.is_optional
        if is_nullable or param.has_default or param.annotation.default_path:
            param_td = param_td.with_optional(True)

        # Build default value
        default_value = None
        if param.has_default and param.default_value is not None:
            with contextlib.suppress(TypeError, ValueError):
                default_value = dagger.JSON(json.dumps(param.default_value))

        return func_def.with_arg(
            param.api_name,
            param_td,
            description=param.annotation.doc,
            default_value=default_value,
            default_path=param.annotation.default_path,
            ignore=list(param.annotation.ignore) if param.annotation.ignore else None,
            deprecated=param.annotation.deprecated,
        )

    def _add_default_constructor(self, type_def: TypeDef, obj: ObjectTypeIR) -> TypeDef:
        """Add a default constructor from fields that have init=True."""
        return_td = dag.type_def().with_object(obj.name)
        func_def = dag.function("", return_td)

        # Add parameters from init fields
        for field in obj.fields:
            if not field.init:
                continue

            param_td = self.resolver.resolve(field.annotation)

            # Handle optional
            if field.has_default or field.annotation.is_optional:
                param_td = param_td.with_optional(True)

            # Build default value
            default_value = None
            if field.has_default and field.default_value is not None:
                with contextlib.suppress(TypeError, ValueError):
                    default_value = dagger.JSON(json.dumps(field.default_value))

            func_def = func_def.with_arg(
                field.api_name,
                param_td,
                description=field.annotation.doc,
                default_value=default_value,
            )

        return type_def.with_constructor(func_def)
