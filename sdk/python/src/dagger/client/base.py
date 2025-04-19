from __future__ import annotations

import enum
import typing

from typing_extensions import override

if typing.TYPE_CHECKING:
    from dagger.client._core import Context
    from dagger.client._session import BaseConnection


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

    def __init__(self, ctx: Context):
        self._ctx = ctx

    def _select(self, *args, **kwargs):
        return self._ctx.select(self._graphql_name(), *args, **kwargs)

    def _select_multiple(self, **kwargs):
        return self._ctx.select_multiple(self._graphql_name(), **kwargs)


class Root(Type):
    """Top level query object type (a.k.a. Query)."""

    @override
    def __init__(self, ctx: Context | None = None):
        if ctx is None:
            from ._core import Context

            ctx = Context()

        super().__init__(ctx)

    @classmethod
    def from_connection(cls, conn: BaseConnection):
        """Create a new instance of the root type, using the given connection."""
        from ._core import Context

        return cls(Context(conn))

    @classmethod
    def _graphql_name(cls) -> str:
        return "Query"
