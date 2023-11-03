import inspect
import logging
import typing
from collections.abc import Sequence

import typing_extensions
from cattrs.preconf.json import make_converter as make_json_converter

from ._types import MissingType, ObjectDefinition
from ._utils import (
    get_doc,
    is_optional,
    non_optional,
    strip_annotations,
    syncify,
)

logger = logging.getLogger(__name__)

if typing.TYPE_CHECKING:
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
        cls = strip_annotations(cls)
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


def to_typedef(annotation: type) -> "TypeDef":  # noqa: C901
    """Convert Python object to API type."""
    assert typing.get_origin(annotation) not in (
        typing.Annotated,
        typing_extensions.Annotated,
    ), "Annotated types should be handled by the caller."

    import dagger
    from dagger.client._guards import is_id_type_subclass

    td = dagger.type_def()

    if is_optional(annotation):
        td = td.with_optional(True)

    annotation = non_optional(annotation)

    if annotation is MissingType:
        return td.with_kind(dagger.TypeDefKind.VoidKind)

    builtins = {
        str: dagger.TypeDefKind.StringKind,
        int: dagger.TypeDefKind.IntegerKind,
        bool: dagger.TypeDefKind.BooleanKind,
    }

    if annotation in builtins:
        return td.with_kind(builtins[annotation])

    if origin := typing.get_origin(annotation):
        # Can't represent unions in the API.
        if origin is typing.Union:
            msg = f"Unsupported union type: {annotation}"
            raise TypeError(msg)

        if issubclass(origin, Sequence):
            of_type, *rest = typing.get_args(annotation)
            if rest:
                msg = (
                    "Unsupported sequence type with multiple "
                    f"element types: {annotation}"
                )
                raise TypeError(msg)

            return td.with_list_of(to_typedef(of_type))

    elif issubclass(annotation, Sequence):
        msg = (
            "Unsupported sequence type without subscripted "
            f"elements type: {annotation}"
        )
        raise TypeError(msg)

    if inspect.isclass(annotation):
        custom_obj: ObjectDefinition | None = getattr(
            annotation, "__dagger_type__", None
        )

        if custom_obj is not None:
            return td.with_object(
                custom_obj.name,
                description=custom_obj.doc,
            )

        if is_id_type_subclass(annotation):
            return td.with_object(annotation.__name__)

        return td.with_object(
            annotation.__name__,
            description=get_doc(annotation),
        )

    msg = f"Unsupported type: {annotation!r}"
    raise TypeError(msg)
