import dataclasses
import functools
import inspect
import logging
import typing
from collections.abc import Collection

from beartype.door import TypeHint
from cattrs.preconf.json import make_converter as make_json_converter

from ._types import ObjectDefinition
from ._utils import (
    get_doc,
    is_annotated,
    is_nullable,
    is_union,
    non_null,
    strip_annotations,
    syncify,
)

logger = logging.getLogger(__name__)

if typing.TYPE_CHECKING:
    from dagger import TypeDef


def make_converter():
    from dagger import dag
    from dagger.client._core import Arg
    from dagger.client._guards import is_id_type, is_id_type_subclass

    conv = make_json_converter(
        detailed_validation=True,
    )

    def dagger_type_structure(id_, cls):
        """Get dagger object type from id."""
        cls = strip_annotations(cls)

        if not is_id_type_subclass(cls):
            msg = f"Unsupported type '{cls.__name__}'"
            raise TypeError(msg)

        return cls(
            dag._select(f"load{cls.__name__}FromID", [Arg("id", id_)])  # noqa: SLF001
        )

    def dagger_type_unstructure(obj):
        """Get id from dagger object."""
        if not is_id_type(obj):
            msg = f"Expected dagger Type object, got `{type(obj)}`"
            raise TypeError(msg)
        return syncify(obj.id)

    conv.register_structure_hook_func(
        is_id_type_subclass,
        dagger_type_structure,
    )

    conv.register_unstructure_hook_func(
        is_id_type_subclass,
        dagger_type_unstructure,
    )

    return conv


@functools.cache
def to_typedef(annotation: type) -> "TypeDef":  # noqa: C901:
    """Convert Python object to API type."""
    assert not is_annotated(
        annotation
    ), "Annotated types should be handled by the caller."

    import dagger
    from dagger import dag
    from dagger.client._guards import is_id_type_subclass
    from dagger.client.base import Enum, Scalar

    td = dag.type_def()

    if isinstance(annotation, dataclasses.InitVar):
        annotation = annotation.type

    typ = TypeHint(annotation)

    if is_nullable(typ):
        td = td.with_optional(True)

    typ = non_null(typ)

    # Can't represent unions in the API.
    if is_union(typ):
        msg = f"Unsupported union type: {typ.hint}"
        raise TypeError(msg)

    if typ is TypeHint(type(None)):
        return td.with_kind(dagger.TypeDefKind.VOID_KIND)

    builtins = {
        str: dagger.TypeDefKind.STRING_KIND,
        int: dagger.TypeDefKind.INTEGER_KIND,
        bool: dagger.TypeDefKind.BOOLEAN_KIND,
    }

    if typ.hint in builtins:
        return td.with_kind(builtins[typ.hint])

    # TODO: Fix when we have support for TypeDefKind.ENUM_KIND in core.
    if issubclass(typ.hint, Enum):
        msg = (
            "Enum types are not supported yet. Define argument as a string"
            " and convert to the desired enum type in the function body."
        )
        raise NotImplementedError(msg)

    # TODO: Fix when we have support for TypeDefKind.SCALAR_KIND in core.
    if issubclass(typ.hint, Scalar):
        msg = (
            "Scalar types are not supported yet. Define argument as a string"
            " and convert to the desired scalar type in the function body."
        )
        raise NotImplementedError(msg)

    # NB: str is a Collection, but we've handled it above.
    if typ.is_subhint(TypeHint(Collection)):
        try:
            return td.with_list_of(to_typedef(typ.args[0]))
        except IndexError:
            msg = (
                "Expected collection type to be subscripted "
                f"with 1 subtype, got {len(typ)}: {typ.hint!r}"
            )
            raise TypeError(msg) from None

    if inspect.isclass(cls := typ.hint):
        custom_obj: ObjectDefinition | None = getattr(cls, "__dagger_type__", None)

        if custom_obj is not None:
            return td.with_object(
                custom_obj.name,
                description=custom_obj.doc,
            )

        if is_id_type_subclass(cls):
            return td.with_object(cls.__name__)

        return td.with_object(
            cls.__name__,
            description=get_doc(cls),
        )

    msg = f"Unsupported type: {typ.hint!r}"
    raise TypeError(msg)
