from typing import Annotated, Protocol, get_args, get_origin, runtime_checkable

from cattrs.preconf.json import make_converter as make_json_converter

from ._utils import asyncify

BUILTINS = {
    str: "String",
    int: "Int",
    float: "Float",
    bool: "Boolean",
}


@runtime_checkable
class GraphQLNamed(Protocol):
    @classmethod
    def graphql_name(cls) -> str:
        ...


def make_converter():
    import dagger
    from dagger.client._guards import is_id_type_subclass

    conv = make_json_converter(omit_if_default=True)

    def dagger_type_structure(id_, cls):
        """Get dagger object type from id."""
        return dagger.client()._get_object_instance(id_, cls)  # noqa: SLF001

    def dagger_type_unstructure(obj):
        """Get id from dagger object."""
        return asyncify(obj.id)

    conv.register_structure_hook_func(
        is_id_type_subclass,
        dagger_type_structure,
    )

    conv.register_unstructure_hook_func(
        is_id_type_subclass,
        dagger_type_unstructure,
    )

    return conv


def to_graphql_representation(obj) -> str:
    """Convert object to GraphQL type as a string."""
    if get_origin(obj) is Annotated:
        # Only support the first argument when annotated.
        obj, *_ = get_args(obj)
    if obj in BUILTINS:
        return BUILTINS[obj]
    return obj.graphql_name() if isinstance(obj, GraphQLNamed) else ""
