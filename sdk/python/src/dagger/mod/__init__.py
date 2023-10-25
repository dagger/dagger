from ._arguments import Argument as Argument
from ._module import Module as Module

_default_mod = Module()

function = _default_mod.function


def default_module() -> Module:
    """Return the default Module builder instance."""
    return _default_mod
