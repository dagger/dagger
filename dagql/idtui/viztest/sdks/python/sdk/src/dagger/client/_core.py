import collections
import dataclasses
import enum
import functools
import json
import logging
import math
import re
import types
import typing
from dataclasses import MISSING
from typing import (
    Any,
    TypeVar,
    overload,
)

import anyio
import cattrs
from cattrs.preconf.json import make_converter as make_json_converter
from typing_extensions import TypeForm

from dagger._exceptions import DaggerError, InvalidQueryError, QueryError
from dagger.client._descriptor import Target, stale_client_error
from dagger.client._session import (
    BaseConnection,
    Session,
    as_session,
    default_session,
)
from dagger.client.base import Input, Scalar, Type

from ._guards import (
    IDType,
    is_id_type,
    is_id_type_sequence,
)

logger = logging.getLogger(__name__)

T = TypeVar("T")
Obj_T = TypeVar("Obj_T", bound=Type)

INDENT = "  "

_ENUM_NAME_RE = re.compile(r"[_A-Za-z][_0-9A-Za-z]*")


class EnumName(str):
    """A schema enum value, for callers that have no generated enum to send."""

    __slots__ = ()


class Arg(typing.NamedTuple):
    name: str  # GraphQL name
    value: Any
    default: Any = MISSING


@dataclasses.dataclass(slots=True)
class Field:
    type_name: str
    name: str
    args: dict[str, Any]
    children: dict[str, "Field"] = dataclasses.field(default_factory=dict)
    # Wraps the children in `... on inline_type { }`.
    inline_type: str | None = None

    def to_graphql(self, alias: str | None = None, depth: int = 1) -> str:
        """Render as a selection, one field per line so error locations line up."""
        pad = INDENT * depth
        out = (
            self.name
            if alias is None or alias == self.name
            else f"{alias}: {self.name}"
        )
        if self.args:
            args = ", ".join(f"{k}: {to_literal(v)}" for k, v in self.args.items())
            out = f"{out}({args})"
        if not self.children:
            return f"{pad}{out}"

        child_depth = depth + 2 if self.inline_type is not None else depth + 1
        children = "\n".join(
            child.to_graphql(child_alias, child_depth)
            for child_alias, child in self.children.items()
        )
        if self.inline_type is not None:
            inner_pad = INDENT * (depth + 1)
            children = (
                f"{inner_pad}... on {self.inline_type} {{\n{children}\n{inner_pad}}}"
            )
        return f"{pad}{out} {{\n{children}\n{pad}}}"

    def add_child(self, child: "Field") -> "Field":
        return dataclasses.replace(self, children={child.name: child})


def to_literal(value: Any) -> str:
    """Render a Python value as a GraphQL literal."""
    if isinstance(value, Input):
        return _input_literal(value)
    if isinstance(value, (list, tuple)):
        return "[" + ", ".join(to_literal(v) for v in value) + "]"
    if isinstance(value, dict):
        return "{" + ", ".join(f"{k}: {to_literal(v)}" for k, v in value.items()) + "}"
    return _scalar_literal(value)


def _scalar_literal(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    # Before str: an enum may subclass str, and goes by name.
    if isinstance(value, (enum.Enum, EnumName)):
        return _enum_literal(value)
    if isinstance(value, str):
        # GraphQL string escapes are a subset of JSON's.
        return json.dumps(value)
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float) and math.isfinite(value):
        return repr(value)
    msg = f"Cannot serialize {value!r} as a GraphQL value"
    raise InvalidQueryError(msg)


def _enum_literal(value: enum.Enum | EnumName) -> str:
    if isinstance(value, enum.Enum):
        return value.name
    # Rendered bare, so anything but a name would inject query syntax.
    if not _ENUM_NAME_RE.fullmatch(value):
        msg = f"Invalid enum value name: {str(value)!r}"
        raise InvalidQueryError(msg)
    return str(value)


def _input_literal(obj: Input) -> str:
    # The generator records each field's GraphQL name.
    names = dict(getattr(type(obj), "_graphql_names", ()))
    fields = []
    for f in dataclasses.fields(obj):
        value = getattr(obj, f.name)
        if f.default is not MISSING and value == f.default:
            continue
        try:
            name = names[f.name]
        except KeyError:
            msg = f"{type(obj).__name__} records no GraphQL name for {f.name!r}"
            raise InvalidQueryError(msg) from None
        fields.append(f"{name}: {to_literal(value)}")
    return "{" + ", ".join(fields) + "}"


def _snapshot(value: Any) -> Any:
    """Copy containers: the query is built later, so mutation must not reach it."""
    if isinstance(value, Input):
        return dataclasses.replace(
            value,
            **{
                f.name: _snapshot(getattr(value, f.name))
                for f in dataclasses.fields(value)
            },
        )
    if isinstance(value, (list, tuple)):
        return [_snapshot(v) for v in value]
    if isinstance(value, dict):
        return {k: _snapshot(v) for k, v in value.items()}
    return value


@dataclasses.dataclass(slots=True)
class Context:
    conn: BaseConnection = dataclasses.field(
        default_factory=default_session,
        compare=False,
    )
    selections: collections.deque[Field] = dataclasses.field(
        default_factory=collections.deque
    )
    converter: cattrs.Converter = dataclasses.field(
        init=False,
        compare=False,
    )
    # On the context because it is what survives chained selections and
    # ID resolution, so it is the only thing that always reaches execute.
    targets: frozenset[Target] = frozenset()

    def __post_init__(self):
        self.converter = make_converter(self)

    def select(
        self,
        type_name: str,
        field_name: str,
        args: typing.Sequence[Arg],
    ) -> "Context":
        args_ = {
            arg.name: _snapshot(arg.value) for arg in args if arg.value != arg.default
        }
        field_ = Field(type_name, field_name, args_)
        selections = self.selections.copy()
        selections.append(field_)
        return dataclasses.replace(self, selections=selections)

    def select_multiple(self, type_name: str, **fields: str) -> "Context":
        selections = self.selections.copy()
        parent = selections.pop()
        # The kwarg names become aliases, so the response already uses the
        # Python names.
        field_ = dataclasses.replace(
            parent,
            children={k: Field(type_name, v, {}) for k, v in fields.items()},
        )
        selections.append(field_)
        return dataclasses.replace(self, selections=selections)

    def root_select(
        self,
        field_name: str,
        args: typing.Sequence[Arg],
    ) -> "Context":
        ctx = dataclasses.replace(self, selections=collections.deque())
        return ctx.select("Query", field_name, args)

    def select_id(self, type_name: str, id_value: str) -> "Context":
        """Load an object by its ID via node(id:) with an inline fragment."""
        ctx = dataclasses.replace(self, selections=collections.deque())
        node_field = Field(
            type_name="Query",
            name="node",
            args={"id": id_value},
            inline_type=type_name,
        )
        selections = ctx.selections.copy()
        selections.append(node_field)
        return dataclasses.replace(ctx, selections=selections)

    def build(self) -> str:
        """Render the selection set as a query document."""
        if not self.selections:
            msg = "No field has been selected"
            raise InvalidQueryError(msg)

        def _collapse(child: Field, field_: Field):
            return field_.add_child(child)

        root = functools.reduce(_collapse, reversed(self.selections))

        return f"query {{\n{root.to_graphql()}\n}}"

    @overload
    async def execute(self, return_type: None = None) -> None: ...

    @overload
    async def execute(self, return_type: TypeForm[T] | type[T]) -> T: ...

    async def execute(
        self, return_type: TypeForm[T] | type[T] | None = None
    ) -> T | None:
        session = as_session(self.conn)
        await self.load_targets(session)
        await self.resolve_ids()
        try:
            result = await session.execute(self.build())
        except QueryError as e:
            if self.targets and (stale := stale_client_error(e, self.targets)):
                raise stale from e
            raise
        return self.get_value(result, return_type) if return_type else None

    async def load_targets(self, session: Session) -> None:
        """Serve every module the query needs."""
        for target in sorted(self.targets, key=lambda t: t.name):
            await session.load(target)

    async def execute_object_list(
        self,
        element_type: type[Obj_T],
    ) -> list[Obj_T]:
        @dataclasses.dataclass
        class Response:
            id: str

        ctx = element_type(self)._select("id", [])  # noqa: SLF001
        ids = await ctx.execute(list[Response])

        gql_name = element_type._graphql_name()  # noqa: SLF001
        return [element_type(ctx.select_id(gql_name, v.id)) for v in ids]

    async def execute_sync(
        self,
        obj: Obj_T,
        field_name: str = "sync",
        args: typing.Sequence[Arg] = (),
    ) -> Obj_T:
        ctx = obj._select(field_name, args)  # noqa: SLF001
        id_ = await ctx.execute(Scalar)
        cls = obj.__class__
        ctx = self.select_id(cls._graphql_name(), id_)
        return cls(ctx)

    @overload
    def get_value(self, value: None, return_type: Any) -> None: ...

    @overload
    def get_value(self, value: dict[str, Any], return_type: type[T]) -> T: ...

    def get_value(self, value: dict[str, Any] | None, return_type: type[T]) -> T | None:
        for f in self.selections:
            if not isinstance(value, dict):
                break
            value = value[f.name]

        if value is None and not _allows_none(return_type):
            msg = (
                "Required field got a null response. Check if parent fields are valid."
            )
            raise InvalidQueryError(msg)

        return self.converter.structure(value, return_type)

    async def resolve_ids(self) -> None:
        """Replace Type arguments with their IDs."""

        # In place, so a forked pipeline doesn't fetch them again.
        async def _resolve_id(pos: int, k: str, v: IDType):
            sel = self.selections[pos]
            sel.args[k] = await v.id()

        async def _resolve_seq_id(pos: int, idx: int, k: str, v: IDType):
            sel = self.selections[pos]
            sel.args[k][idx] = await v.id()

        try:
            async with anyio.create_task_group() as tg:
                for i, sel in enumerate(self.selections):
                    for k, v in sel.args.items():
                        if is_id_type_sequence(v):
                            for seq_i, seq_v in enumerate(v):
                                tg.start_soon(_resolve_seq_id, i, seq_i, k, seq_v)
                        elif is_id_type(v):
                            tg.start_soon(_resolve_id, i, k, v)
        except* DaggerError as grp:
            raise grp.exceptions[0] from None


def _allows_none(t: Any) -> bool:
    if t is None or t is type(None) or t is Any:
        return True
    return typing.get_origin(t) in (typing.Union, types.UnionType) and type(
        None
    ) in typing.get_args(t)


def make_converter(ctx: Context):
    conv = make_json_converter(
        omit_if_default=True,
        detailed_validation=False,
    )

    # Type objects take a Context, which cattrs can't supply.

    def _needs_hook(cls: type) -> bool:
        return issubclass(cls, Type) and hasattr(cls, "__slots__")

    def _struct(d: dict[str, Any], cls: type) -> Any:
        obj = cls(ctx)
        hints = typing.get_type_hints(cls)
        for slot in getattr(cls, "__slots__", ()):
            t = hints.get(slot)
            if t and slot in d:
                setattr(obj, slot, conv.structure(d[slot], t))
        return obj

    conv.register_structure_hook_func(
        _needs_hook,
        _struct,
    )

    configure_converter_enum(conv)

    return conv


def configure_converter_enum(conv: cattrs.Converter, cl: typing.Any = enum.Enum):
    """Register hooks for structuring and destructuring enums using member names."""

    def to_enum_name(val: enum.Enum) -> str:
        return val.name

    def from_enum_name(name: str, cls: type[enum.Enum]) -> enum.Enum:
        return cls[name]

    conv.register_unstructure_hook(cl, to_enum_name)
    conv.register_structure_hook(cl, from_enum_name)
