"""Comparator for differential testing AST analyzer vs runtime introspection.

The contract: for every Python module the AST analyzer accepts, the
schema it derives must match what ``typing.get_type_hints`` plus
``inspect`` would have computed at runtime — modulo a small set of
intentional differences (line numbers, string display of annotations).

This module exposes ``assert_metadata_equivalent`` which compares two
``ModuleMetadata`` instances structurally and raises an ``AssertionError``
with a focused diff when they diverge.
"""

from __future__ import annotations

from typing import Any

from dagger.mod._analyzer.metadata import (
    EnumTypeMetadata,
    FieldMetadata,
    FunctionMetadata,
    ModuleMetadata,
    ObjectTypeMetadata,
    ParameterMetadata,
    ResolvedType,
)


def assert_metadata_equivalent(
    ast_md: ModuleMetadata,
    runtime_md: ModuleMetadata,
    *,
    ignore_default_values: bool = False,
) -> None:
    """Assert AST and runtime metadata describe the same module.

    Args:
        ast_md: Output of ``analyze_source_string``.
        runtime_md: Output of ``runtime_introspect``.
        ignore_default_values: When True, skip default-value comparison.
            Useful when a fixture uses a default the AST analyzer can't
            statically evaluate (function call, lambda, etc.) — the
            runtime sees the actual value, the AST records ``None``.
    """
    diffs: list[str] = []

    _compare_objects(ast_md, runtime_md, diffs, ignore_default_values)
    _compare_enums(ast_md, runtime_md, diffs)

    if diffs:
        msg = "AST analyzer output does not match runtime introspection:\n  " + (
            "\n  ".join(diffs)
        )
        raise AssertionError(msg)


def _compare_objects(
    ast_md: ModuleMetadata,
    runtime_md: ModuleMetadata,
    diffs: list[str],
    ignore_default_values: bool,
) -> None:
    ast_names = set(ast_md.objects)
    runtime_names = set(runtime_md.objects)
    if ast_names != runtime_names:
        only_ast = ast_names - runtime_names
        only_runtime = runtime_names - ast_names
        if only_ast:
            diffs.append(f"Objects only in AST output: {sorted(only_ast)}")
        if only_runtime:
            diffs.append(f"Objects only in runtime output: {sorted(only_runtime)}")
    for name in ast_names & runtime_names:
        _compare_object(
            ast_md.objects[name],
            runtime_md.objects[name],
            diffs,
            ignore_default_values,
        )


def _compare_object(
    ast_obj: ObjectTypeMetadata,
    runtime_obj: ObjectTypeMetadata,
    diffs: list[str],
    ignore_default_values: bool,
) -> None:
    where = f"{ast_obj.name}"
    if ast_obj.is_interface != runtime_obj.is_interface:
        diffs.append(
            f"{where}.is_interface: ast={ast_obj.is_interface} "
            f"runtime={runtime_obj.is_interface}"
        )

    _compare_fields(where, ast_obj.fields, runtime_obj.fields, diffs, ignore_default_values)
    _compare_functions(
        where, ast_obj.functions, runtime_obj.functions, diffs, ignore_default_values
    )


def _compare_fields(
    where: str,
    ast_fields: list[FieldMetadata],
    runtime_fields: list[FieldMetadata],
    diffs: list[str],
    ignore_default_values: bool,
) -> None:
    ast_by_name = {f.python_name: f for f in ast_fields}
    runtime_by_name = {f.python_name: f for f in runtime_fields}

    only_ast = set(ast_by_name) - set(runtime_by_name)
    only_runtime = set(runtime_by_name) - set(ast_by_name)
    if only_ast:
        diffs.append(f"{where}: fields only in AST: {sorted(only_ast)}")
    if only_runtime:
        diffs.append(f"{where}: fields only in runtime: {sorted(only_runtime)}")

    for name in set(ast_by_name) & set(runtime_by_name):
        ast_f = ast_by_name[name]
        rt_f = runtime_by_name[name]
        path = f"{where}.{name}"
        if ast_f.api_name != rt_f.api_name:
            diffs.append(
                f"{path}.api_name: ast={ast_f.api_name!r} runtime={rt_f.api_name!r}"
            )
        _compare_resolved(path, ast_f.resolved_type, rt_f.resolved_type, diffs)
        if ast_f.has_default != rt_f.has_default:
            diffs.append(
                f"{path}.has_default: ast={ast_f.has_default} runtime={rt_f.has_default}"
            )
        if not ignore_default_values:
            _compare_default(path, ast_f.default_value, rt_f.default_value, diffs)


def _compare_functions(
    where: str,
    ast_fns: list[FunctionMetadata],
    runtime_fns: list[FunctionMetadata],
    diffs: list[str],
    ignore_default_values: bool,
) -> None:
    ast_by_name = {f.python_name: f for f in ast_fns}
    runtime_by_name = {f.python_name: f for f in runtime_fns}

    only_ast = set(ast_by_name) - set(runtime_by_name)
    only_runtime = set(runtime_by_name) - set(ast_by_name)
    if only_ast:
        diffs.append(f"{where}: functions only in AST: {sorted(only_ast)}")
    if only_runtime:
        diffs.append(f"{where}: functions only in runtime: {sorted(only_runtime)}")

    for name in set(ast_by_name) & set(runtime_by_name):
        ast_fn = ast_by_name[name]
        rt_fn = runtime_by_name[name]
        path = f"{where}.{name}"
        if ast_fn.api_name != rt_fn.api_name:
            diffs.append(
                f"{path}.api_name: ast={ast_fn.api_name!r} runtime={rt_fn.api_name!r}"
            )
        _compare_resolved(
            f"{path}->return", ast_fn.resolved_return_type, rt_fn.resolved_return_type, diffs
        )
        if ast_fn.is_async != rt_fn.is_async:
            diffs.append(
                f"{path}.is_async: ast={ast_fn.is_async} runtime={rt_fn.is_async}"
            )
        if ast_fn.is_check != rt_fn.is_check:
            diffs.append(
                f"{path}.is_check: ast={ast_fn.is_check} runtime={rt_fn.is_check}"
            )
        _compare_parameters(
            path, ast_fn.parameters, rt_fn.parameters, diffs, ignore_default_values
        )


def _compare_parameters(
    where: str,
    ast_params: list[ParameterMetadata],
    runtime_params: list[ParameterMetadata],
    diffs: list[str],
    ignore_default_values: bool,
) -> None:
    ast_by_name = {p.python_name: p for p in ast_params}
    runtime_by_name = {p.python_name: p for p in runtime_params}

    only_ast = set(ast_by_name) - set(runtime_by_name)
    only_runtime = set(runtime_by_name) - set(ast_by_name)
    if only_ast:
        diffs.append(f"{where}: params only in AST: {sorted(only_ast)}")
    if only_runtime:
        diffs.append(f"{where}: params only in runtime: {sorted(only_runtime)}")

    for name in set(ast_by_name) & set(runtime_by_name):
        ast_p = ast_by_name[name]
        rt_p = runtime_by_name[name]
        path = f"{where}({name})"
        if ast_p.api_name != rt_p.api_name:
            diffs.append(
                f"{path}.api_name: ast={ast_p.api_name!r} runtime={rt_p.api_name!r}"
            )
        _compare_resolved(path, ast_p.resolved_type, rt_p.resolved_type, diffs)
        if ast_p.has_default != rt_p.has_default:
            diffs.append(
                f"{path}.has_default: ast={ast_p.has_default} runtime={rt_p.has_default}"
            )
        if not ignore_default_values:
            _compare_default(path, ast_p.default_value, rt_p.default_value, diffs)
        if ast_p.default_path != rt_p.default_path:
            diffs.append(
                f"{path}.default_path: ast={ast_p.default_path!r} "
                f"runtime={rt_p.default_path!r}"
            )
        if ast_p.default_address != rt_p.default_address:
            diffs.append(
                f"{path}.default_address: ast={ast_p.default_address!r} "
                f"runtime={rt_p.default_address!r}"
            )
        if (ast_p.ignore or None) != (rt_p.ignore or None):
            diffs.append(
                f"{path}.ignore: ast={ast_p.ignore} runtime={rt_p.ignore}"
            )


def _compare_resolved(
    where: str,
    ast_t: ResolvedType,
    runtime_t: ResolvedType,
    diffs: list[str],
) -> None:
    if ast_t.kind != runtime_t.kind:
        diffs.append(f"{where}.kind: ast={ast_t.kind!r} runtime={runtime_t.kind!r}")
    if ast_t.name != runtime_t.name:
        diffs.append(f"{where}.name: ast={ast_t.name!r} runtime={runtime_t.name!r}")
    if ast_t.is_optional != runtime_t.is_optional:
        diffs.append(
            f"{where}.is_optional: ast={ast_t.is_optional} runtime={runtime_t.is_optional}"
        )
    if ast_t.is_self != runtime_t.is_self:
        diffs.append(
            f"{where}.is_self: ast={ast_t.is_self} runtime={runtime_t.is_self}"
        )
    if (ast_t.element_type is None) != (runtime_t.element_type is None):
        diffs.append(
            f"{where}.element_type presence differs: "
            f"ast={ast_t.element_type} runtime={runtime_t.element_type}"
        )
    elif ast_t.element_type is not None and runtime_t.element_type is not None:
        _compare_resolved(
            f"{where}[]", ast_t.element_type, runtime_t.element_type, diffs
        )


def _compare_default(where: str, ast_v: Any, runtime_v: Any, diffs: list[str]) -> None:
    """Compare default values, accepting some structural rephrasing.

    The AST evaluator returns Python values for literals/lists/dicts but
    falls back to ``None`` for things it can't statically evaluate
    (function calls, lambdas) — the comparator's caller skips this
    comparison in those cases via ``ignore_default_values``.
    """
    if ast_v == runtime_v:
        return
    diffs.append(f"{where}.default_value: ast={ast_v!r} runtime={runtime_v!r}")


def _compare_enums(
    ast_md: ModuleMetadata,
    runtime_md: ModuleMetadata,
    diffs: list[str],
) -> None:
    ast_names = set(ast_md.enums)
    runtime_names = set(runtime_md.enums)
    if ast_names != runtime_names:
        only_ast = ast_names - runtime_names
        only_runtime = runtime_names - ast_names
        if only_ast:
            diffs.append(f"Enums only in AST output: {sorted(only_ast)}")
        if only_runtime:
            diffs.append(f"Enums only in runtime output: {sorted(only_runtime)}")
    for name in ast_names & runtime_names:
        _compare_enum(ast_md.enums[name], runtime_md.enums[name], diffs)


def _compare_enum(
    ast_enum: EnumTypeMetadata,
    runtime_enum: EnumTypeMetadata,
    diffs: list[str],
) -> None:
    where = ast_enum.name
    ast_members = {m.name: m.value for m in ast_enum.members}
    runtime_members = {m.name: m.value for m in runtime_enum.members}
    if ast_members != runtime_members:
        diffs.append(
            f"{where}.members: ast={ast_members} runtime={runtime_members}"
        )
