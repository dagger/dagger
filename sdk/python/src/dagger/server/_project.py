import functools
import inspect
import types
from collections.abc import Callable
from dataclasses import dataclass
from typing import (
    Annotated,
    Any,
    get_args,
    get_origin,
    get_type_hints,
)

import strawberry
from strawberry.extensions import FieldExtension
from strawberry.extensions.field_extension import AsyncExtensionResolver
from strawberry.field import StrawberryField
from strawberry.types import Info
from strawberry.utils.await_maybe import await_maybe

from dagger.api.base import Type as DaggerType
from dagger.api.gen import Client

from ._exceptions import BadParameterError, SchemaValidationError
from ._util import has_resolver

_dummy_types = {}


def get_schema(module: types.ModuleType):
    if not (root := get_root(module)):
        return None
    schema = strawberry.Schema(query=root)
    # Remove dummy types that were added to satisfy Strawberry. These
    # should only come from API types that already exist in the schema.
    for type_ in _dummy_types:
        # schema._schema is the generated lower level GraphQL schema.
        # It's not exposed publicly but it's stable. There's no other
        # hook into strawberry to skip validation on an unknown type.
        # The alternative is to remove from the printed schema with a regex.
        del schema._schema.type_map[type_]  # noqa: SLF001
    return schema


def get_root(module: types.ModuleType) -> type | None:
    """The root type is the group of all the top level command functions."""
    if fields := get_commands(module):
        return create_type("Query", fields, extend=True)
    return None


# TODO: Strawberry has a create_type function just like this, but without
# the `extend` parameter. Submit an upstream PR or issue to add it.
def create_type(
    name: str,
    fields: list[StrawberryField],
    extend=False,  # noqa: FBT002
) -> type:
    """Create a strawberry type dynamically."""
    namespace = {}
    annotations = {}

    for field in fields:
        namespace[field.python_name] = field
        annotations[field.python_name] = field.type

    namespace["__annotations__"] = annotations

    cls = types.new_class(name, (), {}, lambda ns: ns.update(namespace))

    return strawberry.type(cls, extend=extend)


def convert_to_strawberry(type_: Any) -> Any:
    """Convert types from user code into Strawberry compatible types."""
    if inspect.isclass(type_) and issubclass(type_, DaggerType):
        # Dagger types already exist in the API but we need to have
        # them in the strawberry schema for validation.
        return get_dummy_type(type_)

    # TODO: default to str because that's the only type that's supported
    # in the API at the moment. Will need to structure/unstructure inside
    # the adapter resolver.
    return type_


def get_dummy_type(cls: type) -> type:
    """Extend an existing type just to get a reference in Strawberry.

    Otherwise it fails validation. We can't use an empty type because
    that's invalid GraphQL, thus Strawberry doesn't allow it also.
    """
    name = cls.__name__
    if name not in _dummy_types:

        @strawberry.field
        def dummy() -> str:
            ...

        _dummy_types[name] = create_type(cls.__name__, [dummy])
    return _dummy_types[name]


def get_commands(module: types.ModuleType):
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


def command(func: Callable[..., Any] | None = None, *, name: str | None = None):
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
        # We could subclass StrawberryResolver and take advantage of its
        # processing logic, but it's better to have as little knowledge of
        # Strawberry internals as possible. We'll just wrap the user's
        # function into something that Strawberry can use without issues.
        signature = inspect.signature(f)
        type_hints = get_type_hints(f, include_extras=True)
        new_params = []
        extensions = []

        for param in signature.parameters.values():
            # TODO: Support default values in schema.
            if param.default is not inspect.Parameter.empty:
                msg = (
                    f"Parameter '{param.name}' has a default value "
                    "which is not supported yet."
                )
                raise BadParameterError(msg, param)

            # TODO: Allow using reserved names by transforming them
            # between strawberry and dagger.
            if param.name in ("root", "info"):
                msg = f"Parameter name '{param.name}' is reserved."
                raise BadParameterError(msg, param)

            # Use type_hints instead of param.annotation to get
            # resolved forward references.
            annotation = type_hints.get(param.name)

            # Exclude the internal client argument from the GraphQL schema.
            if annotation is Client:
                extensions.append(ClientExtension(param.name))
                continue

            # Convenience to replace Annotated[T, "description"] argument type hints
            # with Annotated[T, argument(description="description")].
            if get_origin(annotation) is Annotated:
                match get_args(annotation):
                    case (arg_type, arg_meta) if isinstance(arg_meta, str):
                        annotation = Annotated[arg_type, argument(description=arg_meta)]

            new_params.append(
                param.replace(
                    annotation=convert_to_strawberry(annotation),
                ),
            )

        # Create a new function to change the signature without modifying
        # the original. At the same time we can simplify sync/async resolvers
        # by always exposing to Strawberry an async one. This way we don't
        # have to duplicate sync and async logic in field extensions.
        async def adapter(*args, **kwargs):
            return await await_maybe(f(*args, **kwargs))

        adapter.__signature__ = signature.replace(
            parameters=new_params,
            return_annotation=convert_to_strawberry(
                # type_hints has resolved forward references, unlike
                # signature.return_annotation.
                type_hints.get("return"),
            ),
        )

        # Update annotations from new signature.
        adapter.__annotations__ = get_type_hints(adapter, include_extras=True)

        return strawberry.field(
            resolver=functools.update_wrapper(adapter, f),
            name=name,
            description=inspect.getdoc(f),
            extensions=extensions or None,
        )

    return decorator(func) if func else decorator


@dataclass
class ClientExtension(FieldExtension):
    """Extension to inject the dagger client into a command resolver."""

    name: str

    async def resolve_async(
        self,
        next_: AsyncExtensionResolver,
        source: Any,
        info: Info,
        **kwargs: Any,
    ) -> Any:
        kwargs[self.name] = await info.context.get_client()
        return await next_(source, info, **kwargs)


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
