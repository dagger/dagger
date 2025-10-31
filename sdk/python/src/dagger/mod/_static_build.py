from __future__ import annotations

from typing import Optional

import dagger
from dagger import dag

from ._static_scan import StaticObject, StaticScanResult, scan_src
from ._static_convert import typedef_from_str, doc_from_str


def _parent_package(module_path: str) -> str | None:
    return module_path.rsplit(".", 1)[0] if "." in module_path else None


async def build_module_from_scan(scan: StaticScanResult, main_name: str) -> dagger.ModuleID:
    """Build a Dagger Module typedef from a static scan result.

    This does not import user code. It uses only the collected static metadata
    to construct `dagger.TypeDef` objects and returns the module ID.
    """
    if not main_name:
        raise ValueError("Main object name can't be empty")

    if main_name not in scan.objects:
        # Mirror existing error message style from runtime path
        raise LookupError(
            f"Main object with name '{main_name}' not found or class not decorated with '@dagger.object_type'"
        )

    mod = dag.module()

    # Objects and interfaces
    for obj_name, obj in scan.objects.items():
        # Object/interface type
        type_def = dag.type_def()
        # Description fallback: class docstring, else parent package (__init__.py) doc
        _pkg = _parent_package(obj.module_path)
        _desc = obj.doc if obj.doc else (scan.package_docs.get(_pkg) if _pkg else None)
        if obj.interface:
            type_def = type_def.with_interface(obj_name, description=_desc)
        else:
            type_def = type_def.with_object(obj_name, description=_desc)

        # Fields
        if obj.fields:
            for py_name, field in obj.fields.items():
                td_res = typedef_from_str(field.annotation, scan)
                field_desc = doc_from_str(field.annotation, scan)
                type_def = type_def.with_field(field.name, td_res.td, description=field_desc)

        # Functions and constructor
        # - Regular methods
        for fn_name, fn in obj.functions.items():
            # Return type
            ret_td = typedef_from_str(fn.return_annotation, scan).td
            api_name = fn.api_name or fn.name
            func_def = dag.function(api_name, ret_td)

            # Doc/description
            if fn.doc_override is not None:
                if fn.doc_override:
                    func_def = func_def.with_description(fn.doc_override)
            elif fn.doc:
                func_def = func_def.with_description(fn.doc)

            # Cache policy
            if fn.cache_policy is not None:
                if fn.cache_policy == "never":
                    func_def = func_def.with_cache_policy(dagger.FunctionCachePolicy.Never)
                elif fn.cache_policy == "session":
                    func_def = func_def.with_cache_policy(dagger.FunctionCachePolicy.PerSession)
                elif fn.cache_policy != "":
                    func_def = func_def.with_cache_policy(
                        dagger.FunctionCachePolicy.Default, time_to_live=fn.cache_policy
                    )

            # Parameters
            for p in fn.parameters:
                p_td_res = typedef_from_str(p.annotation, scan)
                arg_td = p_td_res.td
                if p_td_res.is_optional:
                    arg_td = arg_td.with_optional(True)
                func_def = func_def.with_arg(
                    p.name,
                    arg_td,
                    description=p.doc or None,
                    default_value=None,
                    default_path=None,
                    ignore=None,
                )

            type_def = type_def.with_function(func_def)

        # - Constructor for main object only: built from fields with init=True
        if obj_name == main_name and not obj.interface:
            ctor_ret_td = dag.type_def().with_object(obj_name)
            ctor_def = dag.function("", ctor_ret_td)
            # Constructor parameters follow field order and only fields with init=True
            for py_name, field in obj.fields.items():
                # If `init` information exists and is False, skip it; otherwise include
                include = True
                if hasattr(field, "init"):
                    include = bool(getattr(field, "init"))
                if not include:
                    continue
                p_td_res = typedef_from_str(field.annotation, scan)
                arg_td = p_td_res.td
                if p_td_res.is_optional:
                    arg_td = arg_td.with_optional(True)
                ctor_def = ctor_def.with_arg(field.name, arg_td)
            type_def = type_def.with_constructor(ctor_def)

        # Add to module
        if obj.interface:
            mod = mod.with_interface(type_def)
        else:
            mod = mod.with_object(type_def)

    # Enums
    for name, en in scan.enums.items():
        _pkg = _parent_package(en.module_path)
        _desc = en.doc if en.doc else (scan.package_docs.get(_pkg) if _pkg else None)
        enum_def = dag.type_def().with_enum(name, description=_desc)
        for m in en.members:
            enum_def = enum_def.with_enum_member(m.name, value=str(m.value), description=m.description)
        mod = mod.with_enum(enum_def)

    return await mod.id()


async def static_typedefs(project_root: str, main_name: str) -> dagger.ModuleID:
    """High-level helper: scan root `src/` and return a ModuleID of typedefs.

    Parameters
    ----------
    project_root: project root directory that contains `src/`.
    main_name: the main object class name to use as module entrypoint.
    """
    scan = scan_src(project_root)
    return await build_module_from_scan(scan, main_name)


__all__ = ["build_module_from_scan", "static_typedefs"]
