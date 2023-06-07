from collections.abc import Callable
from dataclasses import fields

from cattrs.gen import make_dict_unstructure_fn, override
from cattrs.preconf.json import make_converter

from ._util import has_resolver, is_strawberry_type

converter = make_converter(omit_if_default=True)


def strawberry_unstructure(cls: type) -> Callable:
    """Don't unstructure fields with resolvers."""
    return make_dict_unstructure_fn(
        cls,
        converter,
        **{
            f.name: override(omit_if_default=True, omit=has_resolver(f))
            for f in fields(cls)
        },
    )


converter.register_unstructure_hook_factory(
    is_strawberry_type,
    strawberry_unstructure,
)
