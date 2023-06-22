from collections.abc import Callable
from dataclasses import fields
from typing import TypeVar

import anyio
from cattrs import Converter
from cattrs.gen import make_dict_structure_fn, make_dict_unstructure_fn, override
from cattrs.preconf.json import make_converter
from strawberry.type import has_object_definition

from dagger.api.base import (
    Type as BaseType,
)
from dagger.api.base import (
    is_id_type_subclass,
)

from ._context import Context
from ._util import has_resolver

T = TypeVar("T")
Type = TypeVar("Type", bound=BaseType)

converter = make_converter(omit_if_default=True)


def strawberry_structure_hook(cls: type) -> Callable:
    """Don't structure fields with resolvers."""
    return make_dict_structure_fn(
        cls,
        converter,
        **{
            f.name: override(omit_if_default=True, omit=has_resolver(f))
            for f in fields(cls)
        },
    )


def strawberry_unstructure_hook(cls: type) -> Callable:
    """Don't unstructure with resolvers."""
    return make_dict_unstructure_fn(
        cls,
        converter,
        **{
            f.name: override(omit_if_default=True, omit=has_resolver(f))
            for f in fields(cls)
        },
    )


def register_dagger_type_hooks(conv: Converter, ctx: Context):
    """Register structure and unstructure hooks for dagger types.

    Needs a factory function because of async client access.
    """

    async def get_obj(id_str: str, cls: type[Type]) -> Type:
        """Get dagger object type from string id object class."""
        client = await ctx.get_client()
        return client._get_object_instance(id_str, cls)  # noqa: SLF001

    def dagger_type_structure(obj, cls):
        return anyio.from_thread.run(get_obj, obj, cls)

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


converter.register_structure_hook_factory(
    has_object_definition,
    strawberry_structure_hook,
)

converter.register_unstructure_hook_factory(
    has_object_definition,
    strawberry_unstructure_hook,
)
