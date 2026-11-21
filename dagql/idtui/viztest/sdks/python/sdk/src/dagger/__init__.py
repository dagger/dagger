import gc as _gc
import os as _os

# A module's runtime container runs only the SDK's own short commands, which
# import about 700 modules: collecting while they import costs about 15 ms per
# call. dagger.mod.cli.load_module turns collection back on for user code.
if _os.environ.get("DAGGER_MODULE"):
    _gc.disable()

import contextlib
import functools as _functools
import importlib as _importlib
import importlib.util as _importlib_util
import types as _types
import typing as _typing
import warnings as _warnings

# Make sure to place exceptions first as they're dependencies of other imports.
from dagger._exceptions import *

# Client connection
from dagger.client import Session as Session
from dagger.client import _session as _sessions
from dagger.client._config import Retry as Retry
from dagger.client._connection import connect as connect
from dagger.client._connection import close as close

# The temporary global client is the only generated code named here: it
# keeps dag.container() and dagger.Container working while a module migrates.
if _typing.TYPE_CHECKING:
    try:
        from dagger_global import *
    except ModuleNotFoundError:
        # With the global client, a type checker sees dag as its Client.
        dag = _sessions.default_session()  # type: ignore[assignment, unused-ignore]


@_functools.cache
def _global_client() -> _types.ModuleType | None:
    # On first use, never on import: it imports core and every client, and
    # each of them imports dagger first, so importing it here would fail
    # whenever a generated package is the first import of the process.
    try:
        return _importlib.import_module("dagger_global")
    except ModuleNotFoundError as e:
        # Only its absence means no flag: a global client that cannot import
        # one of its clients is broken, not off.
        if e.name != "dagger_global":
            raise
        return None


def _global_dag() -> Session | None:
    global_ = _global_client()
    return None if global_ is None else global_.dag


_sessions.set_default_finder(_global_dag)

# An earlier version star-imported its one-file bindings from here. Found
# only, never loaded: without this, dag just loses its API with no word why.
if (_legacy := _importlib_util.find_spec("dagger_gen")) is not None:
    _warnings.warn(
        f"{_legacy.origin} is no longer loaded. If an earlier SDK generated "
        "it, `dagger generate` removes it; if you wrote it, delete or rename it.",
        stacklevel=2,
    )
del _legacy

# Module support (only makes sense in a module runtime container)
with contextlib.suppress(ModuleNotFoundError):
    from dagger.mod import *


# Engine provisioning, loaded on first use: a module runtime never provisions,
# and importing it there costs rich and platformdirs on every call.
_PROVISIONING = (
    "Config",
    "Connection",
    "DownloadError",
    "ProvisionError",
    "SessionError",
    "connection",
)


def _provisioning(name: str) -> _typing.Any:
    try:
        provisioning = _importlib.import_module("dagger.provisioning")
    except ModuleNotFoundError as e:
        if not (e.name or "").startswith("dagger.provisioning"):
            raise
        return None
    value = getattr(provisioning, name)
    with contextlib.suppress(AttributeError):
        value.__module__ = __name__
    globals()[name] = value
    return value


def _lazy_names() -> list[str]:
    global_ = _global_client()
    provisioning = (
        _PROVISIONING if _importlib_util.find_spec("dagger.provisioning") else ()
    )
    return [
        "dag",
        "Timeout",
        *provisioning,
        *(() if global_ is None else global_.__all__),
    ]


def __dir__() -> list[str]:
    return sorted({*globals(), *_lazy_names()})


def __getattr__(name: str) -> _typing.Any:
    """Names of the global client, or where a name of the legacy bindings went."""
    if name == "dag":
        return _sessions.default_session()
    if name in _PROVISIONING and (value := _provisioning(name)) is not None:
        return value
    if name == "Timeout":
        # Through the client config, which defines it on first use.
        from dagger.client import _config

        globals()[name] = value = _config.Timeout
        return value
    if name == "__all__":
        # What a star import took when these names were globals.
        public = (n for n in globals() if not n.startswith("_"))
        return sorted({*public, *_lazy_names()})
    global_ = None if name.startswith("_") else _global_client()
    if global_ is not None and name in global_.__all__:
        return getattr(global_, name)
    msg = f"module {__name__!r} has no attribute {name!r}"
    if name == "Client":
        msg += (
            ". dagger.Connection now yields a dagger.Session, "
            "and the API is on core() from dagger_clients.core."
        )
    elif name[:1].isupper():
        # The global client names every type of the core it was generated
        # against, so a name it lacks is a name this engine's core lacks:
        # pointing at the new home would send the reader after nothing.
        if global_ is None:
            msg += (
                f". Core types moved to dagger_clients.core: "
                f"from dagger_clients.core import {name}"
            )
        else:
            msg += (
                f". The core of this engine has no {name}: the code is written "
                "against an API it no longer serves"
            )
    raise AttributeError(msg)


# Re-export imports so they look like they live directly in this package.
for _value in list(locals().values()):
    if getattr(_value, "__module__", "").startswith("dagger."):
        with contextlib.suppress(AttributeError):
            _value.__module__ = __name__
