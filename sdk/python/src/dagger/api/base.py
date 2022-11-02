from collections import deque
from typing import Any, Generic, NamedTuple, Sequence, TypeVar

import attrs
import cattrs
import gql
import graphql
from attrs import define, field
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql

_T = TypeVar("_T")


@define
class Context:
    session: AsyncClientSession
    schema: DSLSchema
    selections: deque[DSLField] = field(factory=deque)
    converter: cattrs.Converter = field(factory=make_converter)

    def select(self, type_name: str, field_name: str, args: dict[str, Any]) -> "Context":
        type_: DSLType = getattr(self.schema, type_name)
        field: DSLField = getattr(type_, field_name)(**args)

        selections = self.selections.copy()
        selections.append(field)

        return attrs.evolve(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not self.selections:
            raise InvalidQueryError("No field has been selected")

        selections = self.selections.copy()
        selectable = selections.pop()

        for dsl_field in reversed(selections):
            selectable = dsl_field.select(selectable)

        return selectable

    def query(self) -> graphql.DocumentNode:
        return dsl_gql(DSLQuery(self.build()))

    async def execute(self, return_type: type[_T]) -> "Result[_T]":
        query = self.query()
        result = await self.session.execute(query, get_execution_result=True)
        value = result.data
        if value is not None:
            value = self.structure_response(value, return_type)
        return Result[_T](value, return_type, self, query, result)

    def structure_response(self, response: dict[str, Any], return_type: type[_T]) -> _T:
        for f in self.selections:
            # FIXME: handle lists
            response = response[f.name]
        return self.converter.structure(response, return_type)


@define
class Result(Generic[_T]):
    value: _T
    type_: type[_T]
    context: Context
    document: graphql.DocumentNode
    result: graphql.ExecutionResult

    @property
    def query(self) -> str:
        return graphql.print_ast(self.document)


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
        assert session.client.schema is not None, "GraphQL session has not been initialized"
        ds = DSLSchema(session.client.schema)
        ctx = Context(session, ds)
        return cls(ctx)

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
