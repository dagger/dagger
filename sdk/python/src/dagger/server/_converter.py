from collections.abc import Callable
from dataclasses import fields
from typing import Annotated, TypeGuard, TypeVar, get_args, get_origin

import anyio
from cattrs import Converter
from cattrs.gen import make_dict_structure_fn, make_dict_unstructure_fn, override
from cattrs.preconf.json import make_converter

from dagger.api.base import IDType, Scalar, Type

from ._context import Context
from ._util import has_resolver, is_strawberry_type

T = TypeVar("T")

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


def is_dagger_id_type(cls: type) -> TypeGuard[IDType]:
    return issubclass(cls, Type) and hasattr(cls, "id")


def register_dagger_type_hooks(conv: Converter, ctx: Context):
    """Register structure and unstructure hooks for dagger types.

    Needs a factory function because of async client access.
    """
    from dagger.api import gen

    # TODO: This is a hack. We need to add functions with codegen for
    # dynamic situations like these.
    #
    # Example:
    # >>> dagger.get_object_from_id(
    # ...     dagger.get_object_id(cls, id_string)
    # ... )
    async def get_obj(id_str: str, cls: type[T]) -> T:
        """Get dagger object type from string id object class."""
        if (o := get_origin(cls)) and o is Annotated:
            cls = get_args(cls)[0]
        try:
            # Get scalar class for object class (e.g., Directory -> DirectoryID).
            id_scalar: type[Scalar] = getattr(gen, f"{cls.__name__}ID")
        except AttributeError as e:
            msg = f"Cannot find ID scalar for {cls.__name__}."
            raise ValueError(msg) from e
        scalar = id_scalar(id_str)

        # Find method in client to get object from ID
        # (e.g., `client.directory(id=DirectoryID(...))`).
        method: Callable[[Scalar], T] = getattr(
            await ctx.get_client(),
            f"{cls.__name__.lower()}",
        )
        return method(scalar)

    def dagger_type_structure(obj, cls):
        return anyio.from_thread.run(get_obj, obj, cls)

    def dagger_type_unstructure(obj):
        return anyio.from_thread.run(obj.id)

    conv.register_structure_hook_func(
        is_dagger_id_type,
        dagger_type_structure,
    )

    conv.register_unstructure_hook_func(
        is_dagger_id_type,
        dagger_type_unstructure,
    )


converter.register_structure_hook_factory(
    is_strawberry_type,
    strawberry_structure_hook,
)

converter.register_unstructure_hook_factory(
    is_strawberry_type,
    strawberry_unstructure_hook,
)
