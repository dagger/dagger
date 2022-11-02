from dataclasses import fields
from typing import Callable

from cattrs.gen import make_dict_unstructure_fn, override
from cattrs.preconf.json import make_converter
from strawberry.field import StrawberryField

converter = make_converter(omit_if_default=True)


def is_strawberry_type(cls: type) -> bool:
    return hasattr(cls, "_type_definition")


def has_resolver(f) -> bool:
    return isinstance(f, StrawberryField) and not f.is_basic_field


def strawberry_unstructure(cls: type) -> Callable:
    """Hook to not unstructure fields with resolvers"""
    return make_dict_unstructure_fn(
        cls, converter, **{f.name: override(omit_if_default=True, omit=has_resolver(f)) for f in fields(cls)}
    )


converter.register_unstructure_hook_factory(
    is_strawberry_type,
    strawberry_unstructure,
)
