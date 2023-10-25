import inspect
import logging
from collections.abc import Sequence
from typing import TYPE_CHECKING, Annotated, get_args, get_origin

from cattrs.preconf.json import make_converter as make_json_converter

from ._types import MissingType
from ._utils import syncify

logger = logging.getLogger(__name__)

if TYPE_CHECKING:
    from dagger import TypeDef


def make_converter():
    import dagger
    from dagger.client._guards import is_id_type, is_id_type_subclass

    conv = make_json_converter(
        omit_if_default=True,
        detailed_validation=True,
    )

    # TODO: register cache volume for custom handling since it's different
    # than the other types.

    def dagger_type_structure(id_, cls):
        """Get dagger object type from id."""
        return dagger.default_client()._get_object_instance(id_, cls)  # noqa: SLF001

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


def to_typedef(typ: type) -> "TypeDef":
    """Convert Python object to API type."""
    import dagger

    td = dagger.type_def()

    if typ is MissingType:
        return td.with_kind(dagger.TypeDefKind.VoidKind)

    # Unwrap Annotated.
    if get_origin(typ) is Annotated:
        typ = get_args(typ)[0]

    builtins = {
        str: dagger.TypeDefKind.StringKind,
        int: dagger.TypeDefKind.IntegerKind,
        bool: dagger.TypeDefKind.BooleanKind,
    }

    if typ in builtins:
        return td.with_kind(builtins[typ])

    if origin := get_origin(typ):
        if issubclass(origin, Sequence):
            of_type, *rest = get_args(typ)
            if rest:
                msg = f"Unsupported sequence type with multiple element types: `{typ}`"
                raise TypeError(msg)

            return td.with_list_of(to_typedef(of_type))

        # TODO: Support Mapping types.

    elif issubclass(typ, Sequence):
        msg = f"Unsupported sequence type without subscripted elements type: `{typ}`"
        raise TypeError(msg)

    if inspect.isclass(typ):
        # TODO: Support custom objects. This is enough for core types though.
        return td.with_object(typ.__name__, description=inspect.getdoc(typ))

    msg = f"Unsupported type: {typ}"
    raise TypeError(msg)
