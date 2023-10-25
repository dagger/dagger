import inspect
from collections.abc import Awaitable
from functools import partial
from typing import Any, TypeAlias, TypeVar, cast

import anyio
import anyio.from_thread
import anyio.to_thread
import cattrs
import cattrs.v

asyncify = anyio.to_thread.run_sync
syncify = anyio.from_thread.run

T = TypeVar("T")

AwaitableOrValue: TypeAlias = Awaitable[T] | T


async def await_maybe(value: AwaitableOrValue[T]) -> T:
    return await value if inspect.isawaitable(value) else cast(T, value)


def transform_error(
    exc: Exception,
    msg: str = "",
    origin: Any | None = None,
    typ: type | None = None,
) -> str:
    """Transform a cattrs error into a list of error messages."""
    path = "$"

    if origin is not None:
        path = getattr(origin, "__qualname__", "")
        if hasattr(origin, "__module__"):
            path = f"{origin.__module__}.{path}"

    fn = partial(cattrs.transform_error, path=path)

    if typ is not None:
        fn = partial(
            fn, format_exception=lambda e, _: cattrs.v.format_exception(e, typ)
        )

    errors = "; ".join(error.removesuffix(" @ $") for error in fn(exc))
    return f"{msg}: {errors}" if msg else errors
