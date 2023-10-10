from ._arguments import Argument as Argument
from ._module import Module as Module

_env = Module()
function = _env.function


def default_module() -> Module:
    """Return the default Module builder instance."""
    return _env
