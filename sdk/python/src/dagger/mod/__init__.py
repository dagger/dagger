from typing_extensions import Doc

from dagger.mod._arguments import Arg
from dagger.mod._arguments import DefaultPath
from dagger.mod._arguments import Ignore
from dagger.mod._arguments import Name
from dagger.mod._module import Module
from dagger.mod._types import Enum


_default_mod = Module()

enum_type = _default_mod.enum_type
function = _default_mod.function
field = _default_mod.field
object_type = _default_mod.object_type


def default_module() -> Module:
    """Return the default Module builder instance."""
    return _default_mod


__all__ = [
    "Arg",
    "DefaultPath",
    "Doc",  # Only re-exported because it's in `typing_extensions`.
    "Ignore",
    "Enum",
    "Name",
    "enum_type",
    "field",
    "function",
    "object_type",
]
