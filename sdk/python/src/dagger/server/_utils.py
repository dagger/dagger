import inspect
from collections.abc import Awaitable
from typing import TypeAlias, TypeVar, cast

T = TypeVar("T")

AwaitableOrValue: TypeAlias = Awaitable[T] | T


async def await_maybe(value: AwaitableOrValue[T]) -> T:
    return await value if inspect.isawaitable(value) else cast(T, value)
