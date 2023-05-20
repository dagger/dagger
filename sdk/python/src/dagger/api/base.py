import contextlib
import enum
import functools
import logging
import typing
from collections import deque
from collections.abc import Callable, Sequence
from typing import Annotated, Any, ParamSpec, TypeGuard, TypeVar, overload

import anyio
import attrs
import cattrs
import graphql
import httpx
from beartype import beartype
from beartype.door import TypeHint
from beartype.roar import BeartypeCallHintViolation
from beartype.vale import Is, IsInstance
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession, SyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql
from gql.transport.exceptions import (
    TransportClosed,
    TransportProtocolError,
    TransportQueryError,
    TransportServerError,
)
from typing_extensions import Self

from dagger.exceptions import (
    ExecuteTimeoutError,
    InvalidQueryError,
    QueryError,
    TransportError,
)

logger = logging.getLogger(__name__)

T = TypeVar("T")
P = ParamSpec("P")


@attrs.define
class Field:
    type_name: str
    name: str
    args: dict[str, Any]

    def to_dsl(self, schema: DSLSchema) -> DSLField:
        type_: DSLType = getattr(schema, self.type_name)
        field: DSLField = getattr(type_, self.name)(**self.args)
        return field


@attrs.define
class Context:
    session: AsyncClientSession | SyncClientSession
    schema: DSLSchema
    selections: deque[Field] = attrs.field(factory=deque)
    converter: cattrs.Converter = attrs.field(
        factory=functools.partial(make_converter, detailed_validation=False)
    )

    def select(
        self,
        type_name: str,
        field_name: str,
        args: dict[str, Any],
    ) -> "Context":
        field = Field(type_name, field_name, args)

        selections = self.selections.copy()
        selections.append(field)

        return attrs.evolve(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not self.selections:
            msg = "No field has been selected"
            raise InvalidQueryError(msg)

        selections = self.selections.copy()
        selectable = selections.pop().to_dsl(self.schema)

        while selections:
            parent = selections.pop().to_dsl(self.schema)
            selectable = parent.select(selectable)

        return selectable

    def query(self) -> graphql.DocumentNode:
        return dsl_gql(DSLQuery(self.build()))

    @overload
    async def execute(self, return_type: None = None) -> None:
        ...

    @overload
    async def execute(self, return_type: type[T]) -> T:
        ...

    async def execute(self, return_type: type[T] | None = None) -> T | None:
        assert isinstance(self.session, AsyncClientSession)
        await self.resolve_ids()
        query = self.query()
        with self._handle_execute(query):
            result = await self.session.execute(query)
        return self.get_value(result, return_type) if return_type else None

    @overload
    def execute_sync(self, return_type: None) -> None:
        ...

    @overload
    def execute_sync(self, return_type: type[T]) -> T:
        ...

    def execute_sync(self, return_type: type[T] | None = None) -> T | None:
        assert isinstance(self.session, SyncClientSession)
        self.resolve_ids_sync()
        query = self.query()
        with self._handle_execute(query):
            result = self.session.execute(query)
        return self.get_value(result, return_type) if return_type else None

    async def resolve_ids(self) -> None:
        """Replace Type object instances with their ID implicitly."""

        # mutating to avoid re-fetching on forked pipeline
        async def _resolve_id(pos: int, k: str, v: IDType):
            sel = self.selections[pos]
            sel.args[k] = await v.id()

        async def _resolve_seq_id(pos: int, idx: int, k: str, v: IDType):
            sel = self.selections[pos]
            sel.args[k][idx] = await v.id()

        # resolve all ids concurrently
        async with anyio.create_task_group() as tg:
            for i, sel in enumerate(self.selections):
                for k, v in sel.args.items():
                    # check if it's a sequence of Type objects
                    if is_id_type_sequence(v):
                        # make sure it's a list, to mutate by index
                        sel.args[k] = list(v)
                        for seq_i, seq_v in enumerate(sel.args[k]):
                            if is_id_type(seq_v):
                                tg.start_soon(_resolve_seq_id, i, seq_i, k, seq_v)
                    elif is_id_type(v):
                        tg.start_soon(_resolve_id, i, k, v)

    def resolve_ids_sync(self) -> None:
        """Replace Type object instances with their ID implicitly."""
        for sel in self.selections:
            for k, v in sel.args.items():
                # check if it's a sequence of Type objects
                if is_id_type_sequence(v):
                    # make sure it's a list, to mutate by index
                    sel.args[k] = list(v)
                    for seq_i, seq_v in enumerate(sel.args[k]):
                        if is_id_type(seq_v):
                            sel.args[k][seq_i] = seq_v.id()
                elif is_id_type(v):
                    sel.args[k] = v.id()

    @contextlib.contextmanager
    def _handle_execute(self, query: graphql.DocumentNode):
        # Reduces duplication when handling errors, between sync and async.
        try:
            yield

        except httpx.TimeoutException as e:
            msg = (
                "Request timed out. Try setting a higher value in 'execute_timeout' "
                "config for this `dagger.Connection()`."
            )
            raise ExecuteTimeoutError(msg) from e

        except httpx.RequestError as e:
            msg = f"Failed to make request: {e}"
            raise TransportError(msg) from e

        except TransportClosed as e:
            msg = (
                "Connection to engine has been closed. Make sure you're "
                "calling the API within a `dagger.Connection()` context."
            )
            raise TransportError(msg) from e

        except (TransportProtocolError, TransportServerError) as e:
            msg = f"Unexpected response from engine: {e}"
            raise TransportError(msg) from e

        except TransportQueryError as e:
            if error := QueryError.from_transport(e, query):
                raise error from e
            raise

    def get_value(self, value: dict[str, Any] | None, return_type: type[T]) -> T:
        if value is not None:
            value = self.structure_response(value, return_type)
        if value is None and not TypeHint(return_type).is_bearable(None):
            msg = (
                "Required field got a null response. Check if parent fields are valid."
            )
            raise InvalidQueryError(msg)
        return value

    def structure_response(self, response: dict[str, Any], return_type: type[T]) -> T:
        for f in self.selections:
            response = response[f.name]
            if response is None:
                return None
        return self.converter.structure(response, return_type)


class Arg(typing.NamedTuple):
    name: str  # GraphQL name
    value: Any
    default: Any = attrs.NOTHING


class Scalar(str):
    """Custom scalar."""


class Enum(str, enum.Enum):
    """Custom enumeration."""

    def __str__(self) -> str:
        return str(self.value)


class Object:
    """Base for object types."""

    @property
    def graphql_name(self) -> str:
        return self.__class__.__name__


class Input(Object):
    """Input object type."""


InputType = Annotated[Input, Is[lambda o: attrs.has(o)]]
InputTypeSeq = Annotated[Sequence[InputType], ~IsInstance[str]]

InputHint = TypeHint(InputType)
InputSeqHint = TypeHint(InputTypeSeq)


def as_input_arg(val):
    if InputHint.is_bearable(val):
        return attrs.asdict(val)
    if InputSeqHint.is_bearable(val):
        return [attrs.asdict(v) for v in val]
    return val


@attrs.define
class Type(Object):
    """Object type."""

    _ctx: Context

    def _select(self, field_name: str, args: typing.Sequence[Arg]) -> Context:
        _args = {
            arg.name: as_input_arg(arg.value)
            for arg in args
            if arg.value is not arg.default
        }
        return self._ctx.select(self.graphql_name, field_name, _args)

    def with_(self, cb: Callable[[Self], Self]) -> Self:
        """
        Use a callable to add to the current object.

        This allows reusing funcionality (e.g., adding mounts)
        without breaking the pipeline chain.

        Example
        -------
        For a :py:class:`Container` type:

        >>> def foobar(c: dagger.Container) -> dagger.Container:
        ...     return c.with_env_variable("FOO", "bar")

        It can be used like this:

        >>> out = await (
        ...     client.container()
        ...     .from_("alpine")
        ...     .with_(foobar)
        ...     .with_exec(["printenv", "FOO"])
        ...     .stdout()
        ... )
        """
        return cb(self)


class Root(Type):
    """Top level query object type (a.k.a. Query)."""

    @classmethod
    def from_session(cls, session: AsyncClientSession):
        assert (
            session.client.schema is not None
        ), "GraphQL session has not been initialized"
        ds = DSLSchema(session.client.schema)
        ctx = Context(session, ds)
        return cls(ctx)

    @property
    def graphql_name(self) -> str:
        return "Query"


@typing.runtime_checkable
class HasID(typing.Protocol):
    async def id(self) -> Scalar:  # noqa: A003
        ...


IDType = Annotated[HasID, IsInstance[Type]]
IDTypeSeq = Annotated[Sequence[IDType], ~IsInstance[str]]

IDTypeHint = TypeHint(IDType)
IDTypeSeqHint = TypeHint(IDTypeSeq)


def is_id_type(v: object) -> TypeGuard[IDType]:
    return IDTypeHint.is_bearable(v)


def is_id_type_sequence(v: object) -> TypeGuard[IDTypeSeq]:
    return IDTypeSeqHint.is_bearable(v)


def typecheck(func: Callable[P, T]) -> Callable[P, T]:
    """
    Runtime type checking.

    Allows fast failure, before sending requests to the API,
    and with greater detail over the specific method and
    parameter with invalid type to help debug.

    This includes catching typos or forgetting to await a
    coroutine, but it's less forgiving in some instances.

    For example, an `args: Sequence[str]` parameter set as
    `args=["echo", 123]` was easily converting the int 123
    to a string by the dynamic query builder. Now it'll fail.
    """
    # Using beartype for the hard work, just tune the traceback a bit.
    # Hiding as **implementation detail** for now. The project is young
    # but very active and with good plans on making it very modular/pluggable.

    # Decorating here allows basic checks during definition time
    # so it'll be catched early, during development.
    bear = beartype(func)

    @functools.wraps(func)
    def wrapper(*args: P.args, **kwargs: P.kwargs) -> T:
        try:
            return bear(*args, **kwargs)
        except BeartypeCallHintViolation as e:
            # Tweak the error message a bit.
            msg = str(e).replace("@beartyped ", "")

            # Everything in `dagger.api.gen.` is exported under `dagger.`.
            msg = msg.replace("dagger.api.gen.", "dagger.")

            # No API methods accept a coroutine, add hint.
            if "<coroutine object" in msg:
                msg = f"{msg} Did you forget to await?"

            # The following `raise` line will show in traceback, keep
            # the noise down to minimum by instantiating outside of it.
            err = TypeError(msg).with_traceback(None)
            raise err from None

    return wrapper
