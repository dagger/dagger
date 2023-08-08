from typing import TypeVar

import anyio
from cattrs import Converter
from cattrs.preconf.json import make_converter

import dagger
from dagger.client._guards import is_id_type_subclass
from dagger.client.base import Type as BaseType

T = TypeVar("T")
Type = TypeVar("Type", bound=BaseType)

converter = make_converter(omit_if_default=True)


def register_dagger_type_hooks(conv: Converter):
    """Register structure and unstructure hooks for dagger types.

    Needs a factory function because of async client access.
    """

    def dagger_type_structure(obj, cls):
        """Get dagger object type from string id object class."""
        return dagger.client()._get_object_instance(obj, cls)  # noqa: SLF001

    def dagger_type_unstructure(obj):
        return anyio.from_thread.run(obj.id)

    conv.register_structure_hook_func(
        is_id_type_subclass,
        dagger_type_structure,
    )

    conv.register_unstructure_hook_func(
        is_id_type_subclass,
        dagger_type_unstructure,
    )
