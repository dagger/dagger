# ruff: noqa: BLE001
from __future__ import annotations

import inspect
import logging
from abc import ABC, abstractmethod
from collections.abc import Callable, Coroutine
from dataclasses import dataclass, field
from functools import cached_property
from typing import (
    TYPE_CHECKING,
    Annotated,
    Any,
    Generic,
    TypeAlias,
    TypeVar,
    get_args,
    get_origin,
    get_type_hints,
    overload,
)

import cattrs
from gql.utils import to_camel_case

from ._arguments import Argument, Parameter
from ._exceptions import FatalError, UserError
from ._types import MissingType
from ._utils import asyncify, await_maybe, transform_error

if TYPE_CHECKING:
    import dagger

    from ._module import Module

logger = logging.getLogger(__name__)

T = TypeVar("T")

ResolverFunc: TypeAlias = Callable[..., Coroutine[Any, Any, T] | T]
DecoratedResolverFunc: TypeAlias = Callable[[ResolverFunc[T]], ResolverFunc[T]]


@dataclass(slots=True)
class Resolver(ABC, Generic[T]):
    """Base class for wrapping user-defined functions."""

    wrapped_func: ResolverFunc[T]
    name: str
    description: str | None = None
    graphql_name: str = field(init=False)

    @classmethod
    def from_callable(
        cls,
        func: ResolverFunc[T],
        name: str | None = None,
        description: str | None = None,
    ):
        """Create a resolver instance from a user-defined function."""
        # TODO: Validate that the function is a callable.

        name = name or func.__name__
        description = description or inspect.getdoc(func)

        try:
            return cls(func, name, description)
        except TypeError as e:
            msg = f"Failed to create resolver for function `{func.__name__}`: {e}"
            raise FatalError(msg) from e

    def __post_init__(self):
        self.graphql_name = to_camel_case(self.name)

    @classmethod
    def to_decorator(cls, mod: Module):
        """Return a decorator for adding a resolver."""

        @overload
        def function(
            func: None = None,
            *,
            name: str | None = None,
            description: str | None = None,
        ) -> DecoratedResolverFunc[T]:
            ...

        @overload
        def function(
            func: ResolverFunc[T],
            *,
            name: None = None,
            description: None = None,
        ) -> ResolverFunc[T]:
            ...

        def function(
            func: ResolverFunc[T] | None = None,
            *,
            name: str | None = None,
            description: str | None = None,
        ) -> ResolverFunc[T] | DecoratedResolverFunc[T]:
            def wrapper(func: ResolverFunc[T]) -> ResolverFunc[T]:
                r = cls.from_callable(func, name, description)
                mod.add_resolver(r)
                return func

            return wrapper(func) if func else wrapper

        function.__module__ = mod.__module__

        return function

    @cached_property
    def signature(self):
        """Return the signature of the wrapped function."""
        return inspect.signature(self.wrapped_func, follow_wrapped=True)

    @property
    def return_type(self):
        """Return the resolved return type of the wrapped function."""
        try:
            return self._type_hints["return"]
        except KeyError:
            return MissingType

    @cached_property
    def parameters(self):
        """Return the parameter annotations of the wrapped function."""
        mapping: dict[str, Parameter] = {}

        for key, value in self.signature.parameters.items():
            name = key
            param = value
            description: str | None = None

            if param.kind is inspect.Parameter.POSITIONAL_ONLY:
                msg = "Positional-only parameters are not supported"
                raise TypeError(msg)

            if param.default is not param.empty:
                logger.warning("Default values are not supported yet")

            try:
                # Use type_hints instead of param.annotation to get
                # resolved forward references.
                annotation = self._type_hints[name]
            except KeyError:
                logger.warning("Missing type annotation for parameter '%s'", name)
                annotation = Any

            if get_origin(annotation) is Annotated:
                args = get_args(annotation)

                # Convenience to replace Annotated[T, "description"] argument
                # type hints with Annotated[T, argument(description="description")].
                match args:
                    case (arg_type, arg_meta) if isinstance(arg_meta, str):
                        description = arg_meta
                        meta = Argument(description=description)
                        annotation = Annotated[arg_type, meta]

                # Extract properties from Argument
                match args:
                    case (arg_type, arg_meta) if isinstance(arg_meta, Argument):
                        name = arg_meta.name or name
                        description = arg_meta.description

            parameter = Parameter(
                name=name,
                signature=param.replace(annotation=annotation),
                description=description,
            )

            mapping[parameter.graphql_name] = parameter

        return mapping

    @cached_property
    def _type_hints(self):
        return get_type_hints(self.wrapped_func, include_extras=True)

    @abstractmethod
    def register(self, typedef: dagger.TypeDef) -> dagger.TypeDef:
        """Add a new object to current module.

        Meant to be overidden by subclasses. Executed when installing the
        module in the API.
        """
        return typedef

    async def convert_arguments(
        self,
        converter: cattrs.Converter,
        raw_args: dict[str, Any],
    ):
        """Convert arguments to the expected parameter types."""
        kwargs: dict[str, Any] = {}

        # Convert arguments to the expected type.
        for gql_name, param in self.parameters.items():
            if gql_name not in raw_args:
                if not param.is_optional:
                    msg = f"Missing required argument `{gql_name}`."
                    raise UserError(msg)
                continue

            value = raw_args[gql_name]
            type_ = param.signature.annotation

            try:
                kwargs[param.name] = await asyncify(converter.structure, value, type_)
            except Exception as e:
                msg = transform_error(
                    e,
                    f"Invalid argument `{param.name}`",
                    self.wrapped_func,
                    type_,
                )
                raise UserError(msg) from e

        # Validate against function signature.
        # Not really necessary, just to make sure.
        try:
            bound_args = self.signature.bind(**kwargs)
        except TypeError as e:
            msg = f"Unable to bind arguments: {e}"
            raise UserError(msg) from e

        return bound_args.arguments

    async def __call__(self, /, *args, **kwargs) -> T:
        # TODO: Reserve __call__ for invoking resolvers within the same module
        # or use a different method for that (e.g., `.call()`)?
        return await await_maybe(self.wrapped_func(*args, **kwargs))
