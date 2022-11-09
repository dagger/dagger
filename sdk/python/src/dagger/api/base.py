import types
import typing
from collections import deque
from typing import Any, NamedTuple, Sequence, TypeVar

import attr
import attrs
import cattrs
import gql
import graphql
from attrs import define
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession, SyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql

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


@define
class Context:
    session: AsyncClientSession | SyncClientSession
    schema: DSLSchema
    selections: deque[Field] = attr.ib(factory=deque)
    converter: cattrs.Converter = attr.ib(factory=make_converter)

    def select(
        self, type_name: str, field_name: str, args: dict[str, Any]
    ) -> "Context":
        field = Field(type_name, field_name, args)

        selections = self.selections.copy()
        selections.append(field)

        return attrs.evolve(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not self.selections:
            raise InvalidQueryError("No field has been selected")

        selections = self.selections.copy()
        selectable = selections.pop().to_dsl(self.schema)

        for field in reversed(selections):
            dsl_field = field.to_dsl(self.schema)
            selectable = dsl_field.select(selectable)

        return selectable

    def query(self) -> graphql.DocumentNode:
        return dsl_gql(DSLQuery(self.build()))

    async def execute(self, return_type: type[_T]) -> _T:
        assert isinstance(self.session, AsyncClientSession)
        query = self.query()
        result = await self.session.execute(query, get_execution_result=True)
        return self._get_value(result.data, return_type)

    def execute_sync(self, return_type: type[_T]) -> _T:
        assert isinstance(self.session, SyncClientSession)
        query = self.query()
        result = self.session.execute(query, get_execution_result=True)
        return self._get_value(result.data, return_type)

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
    name: str
    value: Any
    default: Any = attrs.NOTHING


@define
class Type:
    _ctx: Context

    @property
    def graphql_name(self) -> str:
        return self.__class__.__name__

    def _select(self, field_name: str, args: Sequence[Arg]) -> Context:
        # The use of Arg class here is just to make it easy to pass a
        # dict of arguments without having to be limited to a single
        # `args: dict` parameter in the GraphQL object fields.
        _args = {k: v for k, v, d in args if v is not d}
        return self._ctx.select(self.graphql_name, field_name, _args)


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


class ClientError(Exception):
    """Base class for client errors."""


class InvalidQueryError(ClientError):
    """Misuse of the query builder."""
