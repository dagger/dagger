from collections import deque
from typing import Any, NamedTuple, Sequence, TypeAlias, TypeVar, cast

from attrs import define, evolve, field
from cattrs import Converter
from cattrs.preconf.json import make_converter
from gql.client import AsyncClientSession, SyncClientSession
from gql.dsl import DSLField, DSLQuery, DSLSchema, DSLSelectable, DSLType, dsl_gql
from graphql import DocumentNode

Session: TypeAlias = SyncClientSession | AsyncClientSession

_T = TypeVar("_T")


@define
class Context:
    session: Session
    schema: DSLSchema
    selections: deque[DSLField] = field(factory=deque)
    converter: Converter = field(factory=make_converter)

    def select(self, type_name: str, field_name: str, args: dict[str, Any]) -> "Context":
        type_: DSLType = getattr(self.schema, type_name)
        field: DSLField = getattr(type_, field_name)(**args)

        selections = self.selections.copy()
        selections.append(field)

        return evolve(self, selections=selections)

    def build(self) -> DSLSelectable:
        if not len(self.selections):
            # FIXME: use proper exception class
            raise ValueError("No field has been selected")

        selections = self.selections.copy()
        selectable = selections.pop()

        for dsl_field in reversed(selections):
            selectable = dsl_field.select(selectable)

        return selectable

    def query(self) -> DocumentNode:
        return dsl_gql(DSLQuery(self.build()))

    def execute(self, return_type: type[_T]) -> _T:
        query = self.query()
        response = cast(dict[str, Any], self.session.execute(query))
        for f in self.selections:
            response = response[f.name]
        return self.converter.structure(response, return_type)


_required = object()


class Arg(NamedTuple):
    name: str
    value: Any
    default: Any = _required


@define
class Type:
    _ctx: Context

    @classmethod
    def from_session(cls: type[_T], session: Session) -> _T:
        if session.client.schema is None:
            # FIXME: use proper exception class
            raise ValueError("GraphQL session has not been initialized")
        ds = DSLSchema(session.client.schema)
        ctx = Context(session, ds)
        return cls(ctx)

    def _select(self, field_name: str, args: Sequence[Arg]) -> Context:
        _args = {k: v for k, v, d in args if v is not d}
        return self._ctx.select(self.__class__.__name__, field_name, _args)
