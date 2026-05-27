"""Runtime helpers the generated ``_dagger_main.py`` calls.

This is committed, generic code (not generated). The generated entrypoint
embeds the serialized ModuleTypes JSON and calls ``typedefs_from_json`` for
the def phase — replaying pre-computed TypeDefs with **no** source analysis
at runtime. ``dag`` is imported lazily so importing this module never opens
a connection.

End-to-end behavior (a real ``dag.module()...id()``) is exercised by the
engine integration tests; the unit layer covers the pure renderer.
"""

from __future__ import annotations

import typing

if typing.TYPE_CHECKING:
    import dagger

# Introspection scalar name -> TypeDefKind for builtins.
_BUILTIN_KINDS = {
    "String": "STRING_KIND",
    "Int": "INTEGER_KIND",
    "Float": "FLOAT_KIND",
    "Boolean": "BOOLEAN_KIND",
    "Void": "VOID_KIND",
}


def _typedef_from_ref(ref: dict) -> dagger.TypeDef:
    """Build a ``dagger.TypeDef`` from an introspection ``TypeRef`` dict.

    Inverse of ``_introspect._typeref.type_ref``: a top-level ``NON_NULL``
    means required; a bare ref means optional.
    """
    if ref["kind"] == "NON_NULL":
        return _leaf_typedef(ref["ofType"])

    # Bare (nullable) ref.
    td = _leaf_typedef(ref)
    return td.with_optional(True)


def _leaf_typedef(ref: dict) -> dagger.TypeDef:
    import dagger
    from dagger import dag

    kind = ref["kind"]
    name = ref.get("name")
    td = dag.type_def()

    if kind == "SCALAR":
        builtin = _BUILTIN_KINDS.get(name)
        if builtin is not None:
            return td.with_kind(getattr(dagger.TypeDefKind, builtin))
        return td.with_scalar(name)
    if kind == "LIST":
        return td.with_list_of(_typedef_from_ref(ref["ofType"]))
    if kind == "OBJECT":
        return td.with_object(name)
    if kind == "INTERFACE":
        return td.with_interface(name)
    if kind == "ENUM":
        return td.with_enum(name)

    msg = f"unsupported TypeRef kind: {kind!r}"
    raise ValueError(msg)


async def typedefs_from_json(module_types: dict) -> dagger.ModuleID:
    """Replay a ModuleTypes ``Response`` into engine TypeDefs.

    The def phase of a module-call: no source analysis, just builder calls
    over the values resolved at generate time (so e.g. an ``int`` default of
    ``"20"`` reaches the engine verbatim).
    """
    from dagger import dag

    schema = module_types["__schema"]
    query_name = schema.get("queryType", {}).get("name", "Query")

    mod = dag.module()
    for type_def in schema["types"]:
        if type_def["name"] == query_name:
            # The Query type only carries the constructor field, which the
            # engine derives from the main object; skip it here.
            continue
        if type_def["kind"] == "ENUM":
            mod = mod.with_enum(_enum_typedef(type_def))
        else:
            mod = mod.with_object(_object_typedef(type_def))

    return await mod.id()


def _object_typedef(type_def: dict) -> dagger.TypeDef:
    from dagger import dag

    td = dag.type_def()
    name = type_def["name"]
    td = (
        td.with_interface(name)
        if type_def["kind"] == "INTERFACE"
        else td.with_object(name)
    )
    for field in type_def.get("fields", []):
        td = td.with_function(_function_def(field))
    return td


def _function_def(field: dict):
    from dagger import dag

    fn = dag.function(field["name"], _typedef_from_ref(field["type"]))
    for arg in field.get("args", []):
        import dagger

        default = arg.get("defaultValue")
        fn = fn.with_arg(
            arg["name"],
            _typedef_from_ref(arg["type"]),
            default_value=None if default is None else dagger.JSON(default),
        )
    return fn


def _enum_typedef(type_def: dict) -> dagger.TypeDef:
    from dagger import dag

    td = dag.type_def().with_enum(type_def["name"])
    for member in type_def.get("enumValues", []):
        td = td.with_enum_member(member["name"])
    return td
