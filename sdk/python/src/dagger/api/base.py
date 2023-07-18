import contextlib
import enum
import functools
import inspect
import logging
import typing
from collections import deque
from collections.abc import Callable, Coroutine, Sequence
from dataclasses import MISSING, asdict, dataclass, field, is_dataclass, replace
from typing import (
    Annotated,
    Any,
    ParamSpec,
    TypeGuard,
    TypeVar,
    get_type_hints,
    overload,
)

import anyio
import cattrs
import graphql
import httpx
from beartype import beartype
from beartype.door import TypeHint
from beartype.roar import BeartypeCallHintViolation
from beartype.vale import Is, IsInstance, IsSubclass
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession, SyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql
from gql.transport.exceptions import (
    TransportClosed,
    TransportProtocolError,
    TransportQueryError,
    TransportServerError,
)

from dagger.exceptions import (
    ExecuteTimeoutError,
    InvalidQueryError,
    QueryError,
    TransportError,
)

logger = logging.getLogger(__name__)

T = TypeVar("T")
P = ParamSpec("P")


@dataclass(slots=True)
class Field:
    type_name: str
    name: str
    args: dict[str, Any]
    children: dict[str, "Field"] = field(default_factory=dict)

    def to_dsl(self, schema: DSLSchema) -> DSLField:
        type_: DSLType = getattr(schema, self.type_name)
        field_ = getattr(type_, self.name)(**self.args)
        if self.children:
            field_ = field_.select(
                **{name: child.to_dsl(schema) for name, child in self.children.items()}
            )
        return field_

    def add_child(self, child: "Field") -> "Field":
        return replace(self, children={child.name: child})


@dataclass(slots=True)
class Context:
    session: AsyncClientSession | SyncClientSession
    schema: DSLSchema
    selections: deque[Field] = field(default_factory=deque)
    converter: cattrs.Converter = field(init=False)

    def __post_init__(self):
        conv = make_converter(detailed_validation=False)

        # For types that were returned from a list we need to set
        # their private attributes with a custom structuring function.

        def _needs_hook(cls: type) -> bool:
            return issubclass(cls, Type) and hasattr(cls, "__slots__")

        def _struct(d: dict[str, Any], cls: type) -> Any:
            obj = cls(self)
            hints = get_type_hints(cls)
            for slot in getattr(cls, "__slots__", ()):
                t = hints.get(slot)
                if t and slot in d:
                    setattr(obj, slot, conv.structure(d[slot], t))
            return obj

        conv.register_structure_hook_func(
            _needs_hook,
            _struct,
        )

        self.converter = conv

    def select(
        self, type_name: str, field_name: str, args: dict[str, Any]
    ) -> "Context":
        field_ = Field(type_name, field_name, args)
        selections = self.selections.copy()
        selections.append(field_)
        return replace(self, selections=selections)

    def select_multiple(self, type_name: str, **fields: str) -> "Context":
        selections = self.selections.copy()
        parent = selections.pop()
        # When selecting multiple fields, set them as children of the last
        # selection to make `build` logic simpler.
        field_ = replace(
            parent,
            # Using kwargs for alias names. This way the returned result
            # is already formatted with the python name we expect.
            children={k: Field(type_name, v, {}) for k, v in fields.items()},
        )
        selections.append(field_)
        return replace(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not self.selections:
            msg = "No field has been selected"
            raise InvalidQueryError(msg)

        def _collapse(child: Field, field_: Field):
            return field_.add_child(child)

        # This transforms the selection set into a single root Field, where
        # the `children` attribute is set to the next selection in the set,
        # and so on...
        root = functools.reduce(_collapse, reversed(self.selections))

        # `to_dsl` will cascade to all children, until the end.
        return root.to_dsl(self.schema)

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

    @overload
    def get_value(self, value: None, return_type: Any) -> None:
        ...

    @overload
    def get_value(self, value: dict[str, Any], return_type: type[T]) -> T:
        ...

    def get_value(self, value: dict[str, Any] | None, return_type: type[T]) -> T | None:
        type_hint = TypeHint(return_type)

        for f in self.selections:
            if not isinstance(value, dict):
                break
            value = value[f.name]

        if value is None and not type_hint.is_bearable(value):
            msg = (
                "Required field got a null response. Check if parent fields are valid."
            )
            raise InvalidQueryError(msg)

        return self.converter.structure(value, return_type)

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


class Arg(typing.NamedTuple):
    name: str  # GraphQL name
    value: Any
    default: Any = MISSING


class Scalar(str):
    """Custom scalar."""

    __slots__ = ()


class Enum(str, enum.Enum):
    """Custom enumeration."""

    __slots__ = ()

    def __str__(self) -> str:
        return str(self.value)


class Object:
    """Base for object types."""

    @classmethod
    def _graphql_name(cls) -> str:
        return cls.__name__


class Input(Object):
    """Input object type."""


InputType = Annotated[Input, Is[lambda o: is_dataclass(o)]]
InputTypeSeq = Annotated[Sequence[InputType], ~IsInstance[str]]

InputHint = TypeHint(InputType)
InputSeqHint = TypeHint(InputTypeSeq)


def as_input_arg(val):
    if InputHint.is_bearable(val):
        return asdict(val)
    if InputSeqHint.is_bearable(val):
        return [asdict(v) for v in val]
    return val


class Type(Object):
    """Object type."""

    __slots__ = ("_ctx",)

    def __init__(self, ctx: Context) -> None:
        self._ctx = ctx

    def _select(self, field_name: str, args: typing.Sequence[Arg]):
        _args = {
            arg.name: as_input_arg(arg.value)
            for arg in args
            if arg.value is not arg.default
        }
        return self._ctx.select(self._graphql_name(), field_name, _args)

    def _root_select(self, field_name: str, args: typing.Sequence[Arg]):
        return Root._from_context(self._ctx)._select(field_name, args)  # noqa: SLF001

    def _select_multiple(self, **kwargs):
        return self._ctx.select_multiple(self._graphql_name(), **kwargs)


@typing.runtime_checkable
class FromIDType(typing.Protocol):
    @classmethod
    def _id_type(cls) -> Scalar:
        ...

    @classmethod
    def _from_id_query_field(cls) -> str:
        ...


IDTypeSubclass = Annotated[FromIDType, IsSubclass[Type, FromIDType]]
IDTypeSubclassHint = TypeHint(IDTypeSubclass)


def is_id_type_subclass(v: type) -> TypeGuard[type[IDTypeSubclass]]:
    return IDTypeSubclassHint.is_bearable(v)


_Type = TypeVar("_Type", bound=Type)


class Root(Type):
    """Top level query object type (a.k.a. Query)."""

    @classmethod
    def _graphql_name(cls) -> str:
        return "Query"

    @classmethod
    def from_session(cls, session: AsyncClientSession):
        assert (
            session.client.schema is not None
        ), "GraphQL session has not been initialized"
        ds = DSLSchema(session.client.schema)
        ctx = Context(session, ds)
        return cls(ctx)

    @classmethod
    def _from_context(cls, ctx: Context):
        return cls(replace(ctx, selections=deque()))

    def _get_object_instance(self, id_: str | Scalar, cls: type[_Type]) -> _Type:
        if not is_id_type_subclass(cls):
            msg = f"Unsupported type '{cls.__name__}'"
            raise TypeError(msg)

        if type(id_) is not cls._id_type() and not isinstance(id_, str):
            msg = f"Expected id type '{cls._id_type()}', got '{type(id_)}'"
            raise TypeError(msg)

        assert issubclass(cls, Type)
        ctx = self._select(cls._from_id_query_field(), [Arg("id", id_)])
        return cls(ctx)


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


@overload
def typecheck(
    func: Callable[P, Coroutine[Any, Any, T]]
) -> Callable[P, Coroutine[Any, Any, T]]:
    ...


@overload
def typecheck(func: Callable[P, T]) -> Callable[P, T]:
    ...


def typecheck(
    func: Callable[P, T | Coroutine[Any, Any, T]]
) -> Callable[P, T | Coroutine[Any, Any, T]]:
    ...

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

    @contextlib.contextmanager
    def _handle_exception():
        try:
            yield
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

    if inspect.iscoroutinefunction(bear):

        @functools.wraps(func)
        async def async_wrapper(*args: P.args, **kwargs: P.kwargs) -> T:
            with _handle_exception():
                return await bear(*args, **kwargs)

        return async_wrapper

    if inspect.isfunction(bear):

        @functools.wraps(func)
        def wrapper(*args: P.args, **kwargs: P.kwargs) -> T:
            with _handle_exception():
                return bear(*args, **kwargs)

        return wrapper

    return bear
