import functools
import inspect
from collections.abc import Callable
from inspect import Parameter, getdoc
from types import ModuleType
from typing import (
    Annotated,
    Any,
    cast,
    get_args,
    get_origin,
    get_type_hints,
)

import strawberry
from strawberry.field import StrawberryField
from strawberry.tools import create_type
from strawberry.types import Info
from strawberry.utils.await_maybe import await_maybe

from dagger.api.gen import Client

from ._exceptions import SchemaValidationError
from ._server import Context
from ._util import has_resolver


def get_schema(module: ModuleType):
    root = get_root(module)
    return strawberry.Schema(query=root) if root else None


def get_root(module: ModuleType) -> type | None:
    if fields := get_commands(module):
        return create_type("Query", fields)
    return None


def get_commands(module: ModuleType):
    """Get top-level @command functions in module."""
    return [attr for _, attr in inspect.getmembers(module, has_resolver)]


def commands(cls: type):
    """Class decorator for creating a command group.

    The wrapped class will be turned into a dataclass.

    At least one method with @command must be defined.

    Example:
    >>> @commands
    ... class Test:
    ...     subdir: str
    ...
    ...     @command
    ...     async def unit(self) -> str:
    ...         ...
    """
    if cls.__name__ == "Query":
        msg = (
            "The name 'Query' is reserved. "
            "Please use a different name for your command group."
        )
        raise SchemaValidationError(msg)

    # don't turn non-resolver class attributes into a command
    # (default from strawberry is to use a getattr resolver)
    for name, type_hint in get_type_hints(cls, include_extras=True).items():
        if not isinstance(getattr(cls, name, None), StrawberryField):
            cls.__annotations__[name] = strawberry.Private[type_hint]

    if not any(isinstance(val, StrawberryField) for val in cls.__dict__.values()):
        msg = f"Command group {cls.__name__} must define one or more commands."
        raise SchemaValidationError(msg)

    return strawberry.type(cls)


def command(  # noqa: C901
    func: Callable[..., Any] | None = None, *, name: str | None = None
):
    '''Function decorator for registering a command in the CLI.

    Example:
    >>> @command
    ... async def lint() -> str:
            """Lint code."""
    ...     ...

    The function's docstring will be used as the command's description.

    If it's necessary to override the command's name (e.g., reserved keyword):
    >>> @command(name="import")
    ... async def import_() -> str:
    ...     ...
    '''

    def decorator(f: Callable[..., Any]):
        # replace Annotated[T, "description"] argument type hints
        # with Annotated[T, argument(description="description")]
        # TODO: do this when looping over the function's parameters below.
        type_hints = get_type_hints(f, include_extras=True)
        for arg_name, type_hint in type_hints.items():
            if get_origin(type_hint) is Annotated:
                arg_type, annotation, *_ = get_args(type_hint)
                if isinstance(annotation, str):
                    arg = argument(description=annotation)
                    type_hints[arg_name] = Annotated[arg_type, arg]
        f.__annotations__ = type_hints

        signature = inspect.signature(f)

        strawberry_params = []
        # TODO: Abstract this using converter callbacks.
        resolver_requested_client = None
        # TODO: Allow using reserved names by transforming them
        # between strawberry and dagger.
        reserved_params = ("info", "root")

        for param in signature.parameters.values():
            if param.name in reserved_params:
                msg = f"Parameter name '{param.name}' is reserved."
                raise ValueError(msg)
            if param.annotation is Client:
                resolver_requested_client = param.name
            else:
                # TODO: Support default values in schema.
                if param.default is not Parameter.empty:
                    msg = (
                        f"Parameter '{param.name}' has a default value "
                        "which isn't yet supported."
                    )
                    raise ValueError(msg)
                strawberry_params.append(param)

        # Always add info to get the client in the resolver.
        strawberry_params.append(
            Parameter("info", Parameter.POSITIONAL_OR_KEYWORD, default=None),
        )

        # Make a resolver tailored for strawberry.
        async def strawberry_resolver(*args, **kwargs) -> type | str:
            info = cast(Info[Context, Any], kwargs.pop("info"))
            if param_name := resolver_requested_client:
                kwargs[param_name] = await info.context.get_client()
            bound = signature.bind(*args, **kwargs)
            return await await_maybe(f(*bound.args, **bound.kwargs))

        functools.update_wrapper(strawberry_resolver, f)
        strawberry_resolver.__signature__ = signature.replace(
            parameters=strawberry_params,
        )

        field = strawberry.field(
            resolver=strawberry_resolver,
            name=name,
            description=getdoc(f),
        )

        return cast(StrawberryField, field)

    return decorator(func) if func else decorator


def argument(description: str | None = None, *, name: str | None = None):
    """Metadata for a command flag.

    Only needed when overriding the argument name (e.g., reserved keyword):
    >>> from_: Annotated[str, argument("The address to pull from.", name="from")]

    Not needed if only the description is necessary:
    >>> publish: Annotated[str, "The address to publish the image"]
    """
    return strawberry.argument(
        description=description,
        name=name,
    )
