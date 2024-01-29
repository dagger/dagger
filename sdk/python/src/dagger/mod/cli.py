"""Command line interface for the dagger extension runtime."""
import importlib
import inspect
import logging
import sys
import types
from typing import cast

import rich.traceback
from rich.console import Console

from . import default_module
from ._exceptions import FatalError, UserError
from ._module import Module

errors = Console(stderr=True, style="red")
logger = logging.getLogger(__name__)


def app():
    """Entrypoint for a dagger extension."""
    # TODO: Create custom exception hook to control exit code.
    rich.traceback.install(
        console=errors,
        show_locals=logger.isEnabledFor(logging.DEBUG),
        suppress=[
            "asyncio",
            "anyio",
        ],
    )
    try:
        pymod = import_module()
        mod = get_module(pymod).with_description(inspect.getdoc(pymod))
        mod()
    except FatalError as e:
        if logger.isEnabledFor(logging.DEBUG):
            logger.exception("Fatal error")
        e.rich_print()
        sys.exit(1)


def import_module(module_name: str = "main") -> types.ModuleType:
    """Import python module with given name."""
    # TODO: Allow configuring which package/module to use.
    try:
        return importlib.import_module(module_name)
    except ModuleNotFoundError as e:
        msg = (
            f'The "{module_name}" module could not be found. '
            f'Did you create a "src/{module_name}.py" file in the root of your project?'
        )
        raise UserError(msg) from e


def get_module(module: types.ModuleType) -> Module:
    """Get the environment instance from the main module."""
    # Check for any attribute that is an instance of `Module`.
    mods = (
        cast(Module, attr)
        for name, attr in inspect.getmembers(
            module, lambda obj: isinstance(obj, Module)
        )
        if not name.startswith("_")
    )

    # Use the default module unless the user overrides it with own instance.
    if not (mod := next(mods, None)):
        return default_module()

    # We could pick the first but it can be confusing to ignore the others.
    if next(mods, None):
        cls_path = f"{Module.__module__}.{Module.__qualname__}"
        msg = (
            f"Multiple `{cls_path}` instances were found in module "
            f"{module.__qualname__}. Please ensure that there is only one defined."
        )
        raise UserError(msg)

    return mod
