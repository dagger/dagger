from __future__ import annotations

import inspect
import logging
from collections.abc import Callable, Coroutine
from dataclasses import dataclass, field
from functools import cached_property, partial
from typing import (
    TYPE_CHECKING,
    Annotated,
    Any,
    ClassVar,
    Generic,
    Protocol,
    TypeAlias,
    TypeVar,
    get_args,
    get_origin,
    get_type_hints,
    overload,
    runtime_checkable,
)

import cattrs
from beartype.door import TypeHint
from cattrs.preconf.json import JsonConverter
from gql.utils import to_camel_case

from ._arguments import Argument, Parameter
from ._converter import to_graphql_output_representation
from ._exceptions import FatalError
from ._utils import asyncify, await_maybe

if TYPE_CHECKING:
    import dagger

    from ._environment import Environment

logger = logging.getLogger(__name__)

T = TypeVar("T")

ResolverFunc: TypeAlias = Callable[..., Coroutine[Any, Any, T] | T]
DecoratedResolverFunc: TypeAlias = Callable[[ResolverFunc[T]], ResolverFunc[T]]


@runtime_checkable
class Kind(Protocol):
    """Type of environment resource (e.g., check, artifact, etc).

    They all have a name and a description with these methods.
    """

    def with_name(self, name: str) -> Kind:
        ...

    def with_description(self, description: str) -> Kind:
        ...


K = TypeVar("K", bound=Kind)


@runtime_checkable
class ResultKind(Protocol):
    """Type of environment resource that has a result type."""

    def with_result_type(self, name: str) -> Kind:
        ...


@runtime_checkable
class FlagKind(Protocol):
    """Type of environment resource that has flags."""

    def with_flag(self, name: str, *, description: str | None = None) -> Kind:
        ...


@dataclass(slots=True)
class Resolver(Generic[T]):
    """Base class for wrapping user-defined functions."""

    allowed_return_type: ClassVar[type]
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
        # TODO: validate that the function is a callable

        name = name or func.__name__
        description = description or inspect.getdoc(func)

        return cls(func, name, description)

    def __post_init__(self):
        self.graphql_name = to_camel_case(self.name)

        # Validate the return type of the wrapped function.
        annotated = get_type_hints(type(self)).get("allowed_return_type")

        # Try single type.
        if self.return_type is annotated:
            return

        # Try unions.
        for type_ in get_args(annotated):
            if self.return_type is type_:
                return

        msg = (
            f"Invalid return type for resolver '{self.name}': {self.return_type}. "
            f"Expected {annotated}."
        )
        raise TypeError(msg)

    @classmethod
    def to_decorator(cls) -> ResolverDecorator[T]:
        """Return a descriptor to create a method decorator."""
        return ResolverDecorator[T](cls)

    @cached_property
    def signature(self):
        """Return the signature of the wrapped function."""
        return inspect.signature(self.wrapped_func, follow_wrapped=True)

    @property
    def return_type(self):
        """Return the resolved return type of the wrapped function."""
        return self._type_hints.get("return")

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

    def register(self, env: dagger.Environment) -> dagger.Environment:
        """Add a new resource to current environment.

        Meant to be overidden by subclasses.
        """
        return env

    def configure_kind(self, kind: K) -> K:
        """Configure the newly created resource."""
        kind = kind.with_name(self.graphql_name)
        if self.description:
            kind = kind.with_description(self.description)

        if isinstance(kind, FlagKind):
            for name, param in self.parameters.items():
                kind = kind.with_flag(name, description=param.description)

        if isinstance(kind, ResultKind) and self.return_type:
            if result_type := to_graphql_output_representation(self.return_type):
                kind = kind.with_result_type(result_type)
            else:
                msg = (
                    f"Can't register invalid result type for resolver '{self.name}': "
                    f"{self.return_type}"
                )
                raise TypeError(msg)

        return kind

    async def call(self, converter: JsonConverter, args: dict[str, Any]) -> str:
        """Call the wrapped function."""
        logger.debug("Calling resolver '%s' with arguments: %s", self.name, args)

        kwargs = await self.convert_input(converter, args)
        logger.debug("Converted arguments: %s", kwargs)

        # Call the wrapped function in a separate method to make it easier to override.
        result = await self(**kwargs)

        # Convert the result to a JSON string.
        return await self.convert_output(converter, result)

    async def convert_input(self, converter: JsonConverter, raw_args: dict[str, Any]):
        path = self.wrapped_func.__qualname__
        kwargs: dict[str, Any] = {}

        # Collect all errors before raising an exception.
        errors = set()

        for gql_name, value in raw_args.items():
            name = gql_name

            if name not in self.parameters:
                errors.add(("Unknown argument '%s' in '%s'", name, self.name))

            param = self.parameters[gql_name]
            name = param.python_name
            type_ = param.signature.annotation

            try:
                kwargs[name] = await asyncify(converter.structure, value, type_)
            except cattrs.BaseValidationError as e:
                error_msgs = cattrs.transform_error(e, path=path)
                errors.add(("Invalid argument: %s", "; ".join(error_msgs)))

        if errors:
            for msg, args in errors:
                logger.error(msg, *args)

            msg = f"Invalid arguments for {path}"
            raise FatalError(msg)

        return kwargs

    async def __call__(self, **kwargs):
        # TODO: reserve __call__ for invoking resolvers within the same environment
        return await await_maybe(self.wrapped_func(**kwargs))

    async def convert_output(self, converter: JsonConverter, result: Any) -> str:
        return await asyncify(partial(converter.dumps, result, ensure_ascii=False))


@dataclass
class ResolverDecorator(Generic[T]):
    """Descriptor to create method decorators for registering resolvers."""

    resolver_class: type[Resolver[T]]

    def __get__(self, instance: Environment | None, _):
        if instance:
            self.instance = instance
        return self

    @overload
    def __call__(self, func: ResolverFunc[T]) -> ResolverFunc[T]:
        ...

    @overload
    def __call__(
        self,
        *,
        name: str | None = None,
        description: str | None = None,
    ) -> DecoratedResolverFunc[T]:
        ...

    def __call__(
        self,
        func: ResolverFunc[T] | None = None,
        *,
        name: str | None = None,
        description: str | None = None,
    ) -> ResolverFunc[T] | DecoratedResolverFunc[T]:
        def wrapper(func: ResolverFunc[T]) -> ResolverFunc[T]:
            try:
                r = self.resolver_class.from_callable(func, name, description)
                self.instance._resolvers[r.graphql_name] = r  # noqa: SLF001
            except TypeError:
                logger.exception("Failed to add resolver '%s'", func.__name__)
            return func

        return wrapper(func) if func else wrapper
