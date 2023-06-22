from typing import TypeGuard

from strawberry.field import StrawberryField


def has_resolver(f) -> TypeGuard[StrawberryField]:
    return isinstance(f, StrawberryField) and not f.is_basic_field
