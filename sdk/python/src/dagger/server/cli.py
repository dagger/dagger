"""Command line interface for the dagger extension runtime."""
import inspect
import sys
from importlib import import_module
from typing import cast

from rich.console import Console

from ._environment import Environment
from ._exceptions import FatalError

errors = Console(stderr=True)


def app():
    """Entrypoint for a dagger extension."""
    try:
        env = get_environment()
    except FatalError as e:
        errors.print(e)
        sys.exit(1)

    env()


def get_environment(module_name: str = "main") -> Environment:
    """Get the environment instance from the main module."""
    try:
        module = import_module(module_name)
    except ModuleNotFoundError as e:
        msg = (
            f'The "{module_name}" module could not be found. '
            f'Did you create a "{module_name}.py" file in the root of your project?'
        )
        raise FatalError(msg) from e

    if (env := getattr(module, "env", None)) and isinstance(env, Environment):
        return env

    envs = (
        cast(Environment, attr)
        for name, attr in inspect.getmembers(
            module, lambda obj: isinstance(obj, Environment)
        )
        if not name.startswith("_")
    )

    env = next(envs, None)

    if not env:
        msg = f'No environment was found in module "{module_name}".'
        raise FatalError(msg)

    if next(envs, None):
        msg = (
            f'Multiple environments were found in module "{module_name}". '
            "Please ensure that there is only one environment defined."
        )
        raise FatalError(msg)

    return env
