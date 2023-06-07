import inspect
from typing import TypeGuard

from strawberry.field import StrawberryField


def is_strawberry_type(cls: type) -> bool:
    return inspect.isclass(cls) and hasattr(cls, "_type_definition")


def has_resolver(f) -> TypeGuard[StrawberryField]:
    return isinstance(f, StrawberryField) and not f.is_basic_field
