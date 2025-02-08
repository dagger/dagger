import contextlib

# Make sure to place exceptions first as they're dependencies of other imports.
from dagger._exceptions import *

# Engine provisioning (doesn't make sense in modules)
with contextlib.suppress(ModuleNotFoundError):
    from dagger.provisioning import *

# Client bindings
try:
    from dagger.gen.client import *
except ModuleNotFoundError:
    from dagger.client._gen_placeholder import *

# Client connection
from dagger.client._config import Retry as Retry
from dagger.client._config import Timeout as Timeout
from dagger.client._connection import connect as connect
from dagger.client._connection import close as close

# Module support (only makes sense in a module runtime container)
with contextlib.suppress(ModuleNotFoundError):
    from dagger.mod import *

# Re-export imports so they look like they live directly in this package.
for _value in list(locals().values()):
    if getattr(_value, "__module__", "").startswith("dagger."):
        with contextlib.suppress(AttributeError):
            _value.__module__ = __name__
