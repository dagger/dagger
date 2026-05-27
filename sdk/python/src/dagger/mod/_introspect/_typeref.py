"""Map a live Python annotation to an introspection ``TypeRef`` dict.

The shape mirrors ``cmd/codegen/introspection`` (``TypeRef``: ``kind`` +
optional ``name`` + optional ``ofType``) so the emitted JSON feeds the
engine ``schematool.Merge`` API unchanged. Classification mirrors
``dagger.mod._converter.to_typedef`` exactly, but emits plain dicts (no
engine connection required).
"""

from __future__ import annotations

import enum
import inspect
import typing

from beartype.door import TypeHint

from dagger.client._guards import is_id_type_subclass
from dagger.client.base import Scalar
from dagger.mod._utils import (
    get_object_type,
    is_annotated,
    is_initvar,
    is_nullable,
    is_subclass,
    is_union,
    list_of,
    non_null,
    strip_annotations,
)

# Builtin Python types -> GraphQL scalar names (introspection.Scalar*).
_SCALAR_NAMES: dict[type, str] = {
    str: "String",
    int: "Int",
    float: "Float",
    bool: "Boolean",
    type(None): "Void",
}

TypeRef = dict[str, typing.Any]


def type_ref(annotation: typing.Any, *, optional: bool) -> TypeRef:
    """Return the introspection ``TypeRef`` dict for ``annotation``.

    ``optional`` reflects caller-side optionality (e.g. an argument with a
    default). The annotation's own nullability (``T | None``) is also
    honored. A non-optional, non-nullable type is wrapped in ``NON_NULL``;
    an optional/nullable one is left bare — matching the Go emitter.
    """
    if is_initvar(annotation):
        annotation = annotation.type
    if is_annotated(annotation):
        annotation = strip_annotations(annotation)

    th = TypeHint(annotation)
    nullable = optional or is_nullable(th)
    th = non_null(th)

    if is_union(th):
        msg = f"unsupported type: {th.hint!r}"
        raise TypeError(msg)

    inner = _leaf_ref(th)
    if nullable:
        return inner
    return {"kind": "NON_NULL", "ofType": inner}


def _leaf_ref(th: TypeHint) -> TypeRef:
    hint = th.hint

    if hint in _SCALAR_NAMES:
        return {"kind": "SCALAR", "name": _SCALAR_NAMES[hint]}

    if (element := list_of(hint)) is not None:
        # The list element carries its own optionality (``list[int | None]``
        # -> nullable element), so let ``type_ref`` recompute it.
        return {"kind": "LIST", "ofType": type_ref(element, optional=False)}

    if inspect.isclass(hint):
        name = hint.__name__
        if is_subclass(hint, enum.Enum):
            return {"kind": "ENUM", "name": name}
        if is_subclass(hint, Scalar):
            return {"kind": "SCALAR", "name": name}
        obj_type = get_object_type(hint)
        if obj_type is not None:
            return {
                "kind": "INTERFACE" if obj_type.interface else "OBJECT",
                "name": name,
            }
        if is_id_type_subclass(hint):
            return {"kind": "OBJECT", "name": name}

    msg = f"unsupported type: {hint!r}"
    raise TypeError(msg)
