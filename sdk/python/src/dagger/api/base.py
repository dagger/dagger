import types
import typing
from collections import deque
from typing import Any, NamedTuple, Protocol, Sequence, TypeVar, runtime_checkable

import anyio
import attr
import cattrs
import gql
import graphql
from attrs import define
from beartype.door import is_bearable
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession, SyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql

from dagger.exceptions import DaggerError

_T = TypeVar("_T")


def is_optional(t):
    is_union = typing.get_origin(t) is types.UnionType  # noqa
    return is_union and type(None) in typing.get_args(t)


@define
class Field:
    type_name: str
    name: str
    args: dict[str, Any]

    def to_dsl(self, schema: DSLSchema) -> DSLField:
        type_: DSLType = getattr(schema, self.type_name)
        field: DSLField = getattr(type_, self.name)(**self.args)
        return field


@runtime_checkable
class IDType(Protocol):
    def id(self) -> str:
        ...


@define
class Context:
    session: AsyncClientSession | SyncClientSession
    schema: DSLSchema
    selections: deque[Field] = attr.ib(factory=deque)
    converter: cattrs.Converter = attr.ib(factory=make_converter)

    def select(
        self,
        type_name: str,
        field_name: str,
        args: dict[str, Any],
    ) -> "Context":
        field = Field(type_name, field_name, args)

        selections = self.selections.copy()
        selections.append(field)

        return attr.evolve(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not self.selections:
            raise InvalidQueryError("No field has been selected")

        selections = self.selections.copy()
        selectable = selections.pop().to_dsl(self.schema)

        while selections:
            parent = selections.pop().to_dsl(self.schema)
            selectable = parent.select(selectable)

        return selectable

    def query(self) -> graphql.DocumentNode:
        return dsl_gql(DSLQuery(self.build()))

    async def execute(self, return_type: type[_T]) -> _T:
        assert isinstance(self.session, AsyncClientSession)
        await self.resolve_ids()
        query = self.query()
        result = await self.session.execute(query, get_execution_result=True)
        return self._get_value(result.data, return_type)

    def execute_sync(self, return_type: type[_T]) -> _T:
        assert isinstance(self.session, SyncClientSession)
        self.resolve_ids_sync()
        query = self.query()
        result = self.session.execute(query, get_execution_result=True)
        return self._get_value(result.data, return_type)

    async def resolve_ids(self) -> None:
        # mutating to avoid re-fetching on forked pipeline
        async def _resolve_id(pos: int, k: str, v: IDType):
            sel = self.selections[pos]
            sel.args[k] = await v.id()

        # resolve all ids concurrently
        async with anyio.create_task_group() as tg:
            for i, sel in enumerate(self.selections):
                for k, v in sel.args.items():
                    if isinstance(v, (Type, IDType)):
                        tg.start_soon(_resolve_id, i, k, v)

    def resolve_ids_sync(self) -> None:
        for sel in self.selections:
            for k, v in sel.args.items():
                if isinstance(v, (Type, IDType)):
                    sel.args[k] = v.id()

    def _get_value(self, value: dict[str, Any] | None, return_type: type[_T]) -> _T:
        if value is not None:
            value = self._structure_response(value, return_type)
        if value is None and not is_optional(return_type):
            raise InvalidQueryError(
                "Required field got a null response. Check if parent fields are valid."
            )
        return value

    def _structure_response(
        self, response: dict[str, Any], return_type: type[_T]
    ) -> _T:
        for f in self.selections:
            response = response[f.name]
            if response is None:
                return None
        return self.converter.structure(response, return_type)


class Arg(NamedTuple):
    py_name: str
    name: str
    value: Any
    type_: type
    default: Any = attr.NOTHING

    def is_valid(self) -> bool:
        return is_bearable(self.value, self.type_)


@define
class Type:
    _ctx: Context

    @property
    def graphql_name(self) -> str:
        return self.__class__.__name__

    def _select(self, field_name: str, args: Sequence[Arg]) -> Context:
        return self._ctx.select(
            self.graphql_name,
            field_name,
            self._convert_args(args),
        )

    def _convert_args(self, source: Sequence[Arg]) -> dict[str, Any]:
        args = {}
        for arg in source:
            if arg.value is arg.default:
                continue

            # FIXME: use an exception group
            if not arg.is_valid():
                exp_type = (
                    arg.type_ if typing.get_origin(arg.type_) else arg.type_.__name__
                )
                raise TypeError(
                    f"Wrong type for '{arg.py_name}' parameter. "
                    f"Expected a '{exp_type}' instead."
                )

            args[arg.name] = arg.value
        return args


class Root(Type):
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

    @property
    def _session(self) -> AsyncClientSession:
        return self._ctx.session

    @property
    def _gql_client(self) -> gql.Client:
        return self._session.client

    @property
    def _schema(self) -> graphql.GraphQLSchema:
        return self._ctx.schema._schema


class ClientError(DaggerError):
    """Base class for client errors."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""
