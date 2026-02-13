"""Registration adapter for converting AST metadata to Dagger TypeDefs.

This module provides the bridge between AST analysis results and
the Dagger engine registration API.
"""

from __future__ import annotations

import logging

import dagger
from dagger import dag
from dagger.mod._analyzer.errors import AnalysisError
from dagger.mod._analyzer.metadata import (
    EnumTypeMetadata,
    FunctionMetadata,
    ModuleMetadata,
    ObjectTypeMetadata,
    ParameterMetadata,
    ResolvedType,
)

logger = logging.getLogger(__name__)


async def register_from_metadata(metadata: ModuleMetadata) -> dagger.ModuleID:
    """Register a module with the Dagger engine using AST metadata.

    This is the equivalent of Module._typedefs() but uses pre-analyzed
    metadata instead of runtime introspection.

    Args:
        metadata: The module metadata from AST analysis.

    Returns
    -------
        The registered module ID.
    """
    mod = dag.module()

    # Set module description
    if metadata.doc:
        mod = mod.with_description(metadata.doc)

    # Register object types
    for obj_meta in metadata.objects.values():
        type_def = _build_object_typedef(obj_meta)

        if obj_meta.is_interface:
            mod = mod.with_interface(type_def)
        else:
            mod = mod.with_object(type_def)

    # Register enum types
    for enum_meta in metadata.enums.values():
        enum_def = _build_enum_typedef(enum_meta)
        mod = mod.with_enum(enum_def)

    return await mod.id()


def _build_object_typedef(obj_meta: ObjectTypeMetadata) -> dagger.TypeDef:
    """Build a TypeDef for an object or interface."""
    type_def = dag.type_def()

    if obj_meta.is_interface:
        type_def = type_def.with_interface(
            obj_meta.name,
            description=obj_meta.doc,
        )
    else:
        type_def = type_def.with_object(
            obj_meta.name,
            description=obj_meta.doc,
            deprecated=obj_meta.deprecated,
        )

    # Add fields (only for objects, not interfaces)
    if not obj_meta.is_interface:
        for field_meta in obj_meta.fields:
            field_typedef = _resolved_type_to_typedef(field_meta.resolved_type)
            type_def = type_def.with_field(
                field_meta.api_name,
                field_typedef,
                description=field_meta.doc,
                deprecated=field_meta.deprecated,
            )

    # Add functions
    for func_meta in obj_meta.functions:
        func_def = _build_function_def(func_meta)
        type_def = type_def.with_function(func_def)

    # Add constructor (for objects only)
    has_constructor = (
        obj_meta.constructor
        and not obj_meta.is_interface
        and obj_meta.constructor.parameters
    )
    if has_constructor:
        ctor_def = _build_function_def(obj_meta.constructor)  # type: ignore[arg-type]
        type_def = type_def.with_constructor(ctor_def)

    return type_def


def _build_function_def(func_meta: FunctionMetadata) -> dagger.Function:
    """Build a Function definition."""
    return_typedef = _resolved_type_to_typedef(func_meta.resolved_return_type)

    func_def = dag.function(
        func_meta.api_name,
        return_typedef,
    )

    if func_meta.doc:
        func_def = func_def.with_description(func_meta.doc)

    if func_meta.deprecated:
        func_def = func_def.with_deprecated(reason=func_meta.deprecated)

    if func_meta.is_check:
        func_def = func_def.with_check()

    if func_meta.is_generate:
        func_def = func_def.with_generator()

    # Handle cache policy
    if func_meta.cache_policy is not None:
        if func_meta.cache_policy == "never":
            func_def = func_def.with_cache_policy(dagger.FunctionCachePolicy.Never)
        elif func_meta.cache_policy == "session":
            func_def = func_def.with_cache_policy(dagger.FunctionCachePolicy.PerSession)
        elif func_meta.cache_policy != "":
            func_def = func_def.with_cache_policy(
                dagger.FunctionCachePolicy.Default,
                time_to_live=func_meta.cache_policy,
            )

    # Add parameters
    for param_meta in func_meta.parameters:
        func_def = _add_parameter(func_def, param_meta)

    return func_def


def _add_parameter(
    func_def: dagger.Function, param_meta: ParameterMetadata
) -> dagger.Function:
    """Add a parameter to a function definition."""
    arg_typedef = _resolved_type_to_typedef(param_meta.resolved_type)

    # Note: optionality from type (T | None) is already handled by
    # _resolved_type_to_typedef via resolved.is_optional.

    # Convert default value to JSON if present
    default_value = None
    if param_meta.default_value is not None:
        import contextlib
        import json

        with contextlib.suppress(TypeError, ValueError):
            default_value = dagger.JSON(json.dumps(param_meta.default_value))

    return func_def.with_arg(
        param_meta.api_name,
        arg_typedef,
        description=param_meta.doc,
        default_value=default_value,
        default_path=param_meta.default_path,
        default_address=param_meta.default_address,
        ignore=param_meta.ignore,
        deprecated=param_meta.deprecated,
    )


def _build_enum_typedef(enum_meta: EnumTypeMetadata) -> dagger.TypeDef:
    """Build a TypeDef for an enum."""
    enum_def = dag.type_def().with_enum(
        enum_meta.name,
        description=enum_meta.doc,
    )

    for member in enum_meta.members:
        enum_def = enum_def.with_enum_member(
            member.name,
            value=member.value,
            description=member.doc,
            deprecated=member.deprecated,
        )

    return enum_def


def _resolved_type_to_typedef(resolved: ResolvedType) -> dagger.TypeDef:  # noqa: PLR0911
    """Convert a ResolvedType to a Dagger TypeDef."""
    td = dag.type_def()

    # Handle optional
    if resolved.is_optional:
        td = td.with_optional(True)

    # Handle different kinds
    if resolved.kind == "primitive":
        kind_map = {
            "str": dagger.TypeDefKind.STRING_KIND,
            "int": dagger.TypeDefKind.INTEGER_KIND,
            "float": dagger.TypeDefKind.FLOAT_KIND,
            "bool": dagger.TypeDefKind.BOOLEAN_KIND,
            "bytes": dagger.TypeDefKind.STRING_KIND,  # bytes as string
            "Any": dagger.TypeDefKind.STRING_KIND,  # Any as string fallback
        }
        type_kind = kind_map.get(resolved.name, dagger.TypeDefKind.STRING_KIND)
        return td.with_kind(type_kind)

    if resolved.kind == "void":
        return td.with_kind(dagger.TypeDefKind.VOID_KIND)

    if resolved.kind == "list":
        if resolved.element_type:
            element_td = _resolved_type_to_typedef(resolved.element_type)
            return td.with_list_of(element_td)
        # List without element type - fallback to string
        return td.with_list_of(dag.type_def().with_kind(dagger.TypeDefKind.STRING_KIND))

    if resolved.kind == "object":
        return td.with_object(resolved.name)

    if resolved.kind == "interface":
        return td.with_interface(resolved.name)

    if resolved.kind == "enum":
        return td.with_enum(resolved.name)

    if resolved.kind == "scalar":
        return td.with_scalar(resolved.name)

    msg = f"Unknown type kind: {resolved.kind}"
    raise AnalysisError(msg)
