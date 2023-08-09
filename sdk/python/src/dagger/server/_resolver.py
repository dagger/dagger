from __future__ import annotations

import inspect
from collections.abc import Callable, Coroutine
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Generic, Protocol, TypeAlias, TypeVar

from ._utils import await_maybe

if TYPE_CHECKING:
    import dagger


T = TypeVar("T")

ResolverFunc: TypeAlias = Callable[..., Coroutine[Any, Any, T] | T]
DecoratedResolverFunc: TypeAlias = Callable[[ResolverFunc[T]], ResolverFunc[T]]


class Kind(Protocol):
    def with_name(self, name: str) -> Kind:
        ...

    def with_description(self, description: str) -> Kind:
        ...


K = TypeVar("K", bound=Kind)


@dataclass(slots=True)
class Resolver(Generic[T]):
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
        name = name or func.__name__
        description = description or inspect.getdoc(func)
        return cls(func, name, description)

    def register(self, env: dagger.Environment) -> dagger.Environment:
        return env

    def create_kind(self, kind: K) -> K:
        kind = kind.with_name(self.name)
        if self.description:
            kind = kind.with_description(self.description)
        return kind

    async def call(self, *args, **kwargs):
        return await await_maybe(self.wrapped_func(*args, **kwargs))


"""
@dataclass(slots=True)
class ResolverDescriptor:
    resolver_class: type[Resolver[T]]

    def __get__(self, instance, owner):
        if instance is None:
            return self

        def decorator(
            resolver_func: ResolverFunc[T] | None = None,
            *,
            name: str | None = None,
            description: str | None = None,
        ) -> :
            def wrapper(func: ResolverFunc[T]):
                return self.resolver_class.from_callable(
                    func, name=name, description=description
                )
            return self.resolver_class.from_callable(instance)

"""
