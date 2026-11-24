"""The calls generated client code makes into the SDK."""

import dataclasses
from typing import TypeVar

from dagger.client._core import Arg, Context
from dagger.client._descriptor import Target
from dagger.client._session import Session, default_session
from dagger.client.base import Type

T = TypeVar("T", bound=Type)


def client_root(
    cls: type[T],
    target: Target | None,
    field: str | None,
    args: list[Arg],
    *,
    session: Session | None = None,
) -> T:
    """The root of a client: what to load, and the entry field if any.

    Core has neither, so ``core()`` passes ``None`` for both.
    """
    ctx = Context(
        session or default_session(),
        targets=frozenset([target]) if target else frozenset(),
    )
    if field is not None:
        ctx = ctx.root_select(field, args)
    return cls(ctx)


def client_select(
    receiver: Type,
    target: Target,
    field: str,
    args: list[Arg],
) -> Context:
    """Select a field a client adds to the receiver's type."""
    ctx = receiver._ctx  # noqa: SLF001
    ctx = dataclasses.replace(ctx, targets=ctx.targets | {target})
    return ctx.select(receiver._graphql_name(), field, args)  # noqa: SLF001
