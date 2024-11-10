from __future__ import annotations

import collections
import dataclasses
import enum

from dagger.client._core import Context
from dagger.client._session import BaseConnection, SharedConnection


class Scalar(str):
    """Custom scalar."""

    __slots__ = ()


class Enum(str, enum.Enum):
    """Custom enumeration."""

    __slots__ = ()

    def __str__(self) -> str:
        """The string representation of the enum value."""
        return str(self.value)


class Object:
    """Base for object types."""

    __slots__ = ()

    @classmethod
    def _graphql_name(cls) -> str:
        return cls.__name__


class Input(Object):
    """Input object type."""

    __slots__ = ()


class Type(Object):
    """Object type."""

    __slots__ = ("_ctx",)

    def __init__(self, ctx: Context | None = None):
        # Since SharedConnection is a singleton, we could make Context optional
        # in every Type like here, but let's keep it only for the root object for now.
        self._ctx = ctx or Context(SharedConnection())

    def _select(self, *args, **kwargs):
        return self._ctx.select(self._graphql_name(), *args, **kwargs)

    def _select_multiple(self, **kwargs):
        return self._ctx.select_multiple(self._graphql_name(), **kwargs)


class Root(Type):
    """Top level query object type (a.k.a. Query)."""

    @classmethod
    def _graphql_name(cls) -> str:
        return "Query"

    @classmethod
    def from_context(cls, ctx: Context):
        return cls(dataclasses.replace(ctx, selections=collections.deque()))

    @classmethod
    def from_connection(cls, conn: BaseConnection):
        return cls(Context(conn))
