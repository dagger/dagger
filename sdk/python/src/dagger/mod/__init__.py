from typing_extensions import Doc


from ._arguments import Arg as Arg
from ._module import Module as Module
from ._types import Enum as Enum

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
    "Doc",  # Only re-exported because it's in `typing_extensions`.
    "Enum",
    "enum_type",
    "field",
    "function",
    "object_type",
]
