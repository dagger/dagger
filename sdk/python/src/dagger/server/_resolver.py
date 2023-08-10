from __future__ import annotations

import inspect
from collections.abc import Callable, Coroutine
from dataclasses import dataclass
from functools import cached_property
from typing import (
    TYPE_CHECKING,
    Any,
    ClassVar,
    Generic,
    Protocol,
    TypeAlias,
    TypeVar,
    get_origin,
    get_type_hints,
    overload,
    runtime_checkable,
)

from beartype.door import TypeHint
from gql.utils import to_camel_case
from typing_extensions import Self

from ._utils import await_maybe

if TYPE_CHECKING:
    import dagger

    from ._environment import Environment


T = TypeVar("T")

ResolverFunc: TypeAlias = Callable[..., Coroutine[Any, Any, T] | T]
DecoratedResolverFunc: TypeAlias = Callable[[ResolverFunc[T]], ResolverFunc[T]]


BUILTINS = {
    str: "String",
    int: "Int",
    float: "Float",
    bool: "Boolean",
}


@runtime_checkable
class GraphQLNamed(Protocol):
    @classmethod
    def graphql_name(cls) -> str:
        ...


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


@dataclass(slots=True)
class Resolver(Generic[T]):
    """Base class for wrapping user-defined functions."""

    allowed_return_type: ClassVar[type]
    wrapped_func: ResolverFunc[T]
    name: str
    description: str | None

    @classmethod
    def from_callable(
        cls,
        func: ResolverFunc[T],
        name: str | None = None,
        description: str | None = None,
    ):
        """Create a resolver instance from a user-defined function."""
        name = name or func.__name__
        description = description or inspect.getdoc(func)
        return cls(func, name, description).validate()

    def validate(self) -> Self:
        if self.allowed_return_hint and not self.allowed_return_hint.is_bearable(
            self.return_type
        ):
            msg = f"Invalid return type for resolver '{self.name}': {self.return_type}"
            raise TypeError(msg)
        return self

    @cached_property
    def allowed_return_hint(self):
        annotated = get_type_hints(type(self)).get("allowed_return_type")

        if get_origin(annotated) is ClassVar:
            return TypeHint(type)

        # TODO: use beartype validators instead
        return TypeHint(annotated)

    @classmethod
    def to_decorator(cls) -> ResolverDecorator[T]:
        """Return a descriptor to create a method decorator."""
        return ResolverDecorator[T](cls)

    @property
    def graphql_name(self) -> str:
        """Return the name of the resolver as it should appear in the schema."""
        return to_camel_case(self.name)

    @cached_property
    def signature(self):
        """Return the signature of the wrapped function."""
        return inspect.signature(self.wrapped_func, follow_wrapped=True)

    @property
    def return_type(self):
        """Return the resolved return type of the wrapped function."""
        return self._type_hints.get("return")

    @property
    def parameters(self):
        """Return the parameter annotations of the wrapped function."""
        return {k: v for k, v in self._type_hints.items() if k != "return"}

    @cached_property
    def _type_hints(self):
        return get_type_hints(self.wrapped_func, include_extras=True)

    def register(self, env: dagger.Environment) -> dagger.Environment:
        """Add a new resource to current environment."""
        return env

    def configure_kind(self, kind: K) -> K:
        """Configure the newly created resource."""
        kind = kind.with_name(self.graphql_name)
        if self.description:
            kind = kind.with_description(self.description)
        if isinstance(kind, ResultKind) and self.return_type:
            if self.return_type in BUILTINS:
                kind = kind.with_result_type(BUILTINS[self.return_type])
            elif isinstance(self.return_type, GraphQLNamed):
                kind = kind.with_result_type(self.return_type.graphql_name())
        return kind

    async def call(self, *args, **kwargs):
        """Call the wrapped function."""
        return await await_maybe(self.wrapped_func(*args, **kwargs))


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
            r = self.resolver_class.from_callable(func, name, description)
            self.instance._resolvers[r.graphql_name] = r  # noqa: SLF001
            return func

        return wrapper(func) if func else wrapper
