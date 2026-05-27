"""Serialize a live Dagger module to schematool ModuleTypes JSON.

Walks the runtime-introspected registry a ``Module`` builds at import time
(``_objects`` / ``_enums``) and emits the same introspection ``Response``
shape the Go SDK's emitter produces, so it feeds the engine
``schematool.Merge`` API unchanged. Defaults are taken from the live
``Parameter.default_value`` (cattrs-serialized), so a ``logging.INFO``
default is recorded as ``"20"`` — the runtime value the engine expects.
"""

from __future__ import annotations

import typing

from dagger.mod._introspect._typeref import type_ref
from dagger.mod._utils import to_camel_case, to_pascal_case

if typing.TYPE_CHECKING:
    from dagger.mod._module import Module
    from dagger.mod._resolver import Function, ObjectType


def live_to_introspection_json(
    module: Module,
    *,
    main_object_name: str,
    module_name: str,
) -> dict:
    """Return the schematool ModuleTypes ``Response`` for ``module``."""
    objects = module._objects  # noqa: SLF001 — same-package registry access
    enums = module._enums  # noqa: SLF001 — same-package registry access

    types: list[dict] = [
        _object_type(name, obj_type) for name, obj_type in objects.items()
    ]
    types += [_enum_type(name, enum_cls) for name, enum_cls in enums.items()]
    types.append(_query_type(objects, main_object_name, module_name))

    return {
        "__schema": {
            "queryType": {"name": "Query"},
            "types": types,
            "directives": [],
        },
        "__schemaVersion": "",
    }


def _object_type(name: str, obj_type: ObjectType) -> dict:
    fields: list[dict] = [
        {
            "name": field.name,
            "type": type_ref(field.return_type, optional=False),
            "args": [],
        }
        for field in obj_type.fields.values()
    ]
    # The empty-named function is the constructor; it's carried on Query for
    # the main object, not as a field here.
    fields += [
        _function_field(fn)
        for api_name, fn in obj_type.functions.items()
        if api_name != ""
    ]

    return {
        "kind": "INTERFACE" if obj_type.interface else "OBJECT",
        "name": name,
        "fields": fields,
    }


def _function_field(fn: Function) -> dict:
    return {
        "name": fn.name,
        "type": type_ref(fn.return_type, optional=False),
        "args": [_input_value(param) for param in fn.parameters.values()],
    }


def _input_value(param: typing.Any) -> dict:
    default = None if param.default_value is None else str(param.default_value)
    return {
        "name": param.name,
        "type": type_ref(param.resolved_type, optional=param.is_optional),
        "defaultValue": default,
    }


def _enum_type(name: str, enum_cls: type) -> dict:
    return {
        "kind": "ENUM",
        "name": name,
        "enumValues": [
            {"name": member.name} for member in enum_cls.__members__.values()
        ],
    }


def _query_type(
    objects: dict[str, ObjectType], main_object_name: str, module_name: str
) -> dict:
    args: list[dict] = []
    main = objects.get(main_object_name)
    if main is not None:
        ctor = main.functions.get("")
        if ctor is not None:
            args = [_input_value(p) for p in ctor.parameters.values()]

    return {
        "kind": "OBJECT",
        "name": "Query",
        "fields": [
            {
                "name": to_camel_case(module_name),
                "type": {
                    "kind": "NON_NULL",
                    "ofType": {
                        "kind": "OBJECT",
                        "name": to_pascal_case(main_object_name),
                    },
                },
                "args": args,
            }
        ],
    }
