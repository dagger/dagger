import enum
import functools
import logging
import re
import textwrap
from abc import ABC, abstractmethod
from datetime import date, datetime, time
from decimal import Decimal
from functools import partial
from itertools import chain, groupby
from keyword import iskeyword
from operator import attrgetter
from typing import (
    Any,
    Callable,
    ClassVar,
    Generic,
    Iterator,
    ParamSpec,
    Protocol,
    TypeAlias,
    TypeGuard,
    TypeVar,
    cast,
)

import attrs
from graphql import (
    GraphQLArgument,
    GraphQLEnumType,
    GraphQLField,
    GraphQLFieldMap,
    GraphQLInputField,
    GraphQLInputFieldMap,
    GraphQLInputObjectType,
    GraphQLInputType,
    GraphQLLeafType,
    GraphQLList,
    GraphQLNamedType,
    GraphQLNonNull,
    GraphQLObjectType,
    GraphQLOutputType,
    GraphQLScalarType,
    GraphQLSchema,
    GraphQLType,
    GraphQLWrappingType,
    Undefined,
    get_named_type,
    is_leaf_type,
)
from graphql.pyutils import camel_to_snake

ACRONYM_RE = re.compile(r"([A-Z\d]+)(?=[A-Z\d]|$)")
"""Pattern for grouping initialisms."""

DEPRECATION_RE = re.compile(r"`([a-zA-Z\d_]+)`")
"""Pattern for extracting replaced references in deprecations."""

logger = logging.getLogger(__name__)

indent = partial(textwrap.indent, prefix=" " * 4)
wrap = textwrap.wrap
wrap_indent = partial(wrap, initial_indent=" " * 4, subsequent_indent=" " * 4)


T_ParamSpec = ParamSpec("T_ParamSpec")

IDName: TypeAlias = str
TypeName: TypeAlias = str
IDMap: TypeAlias = dict[IDName, TypeName]


class Scalars(enum.Enum):
    ID = str
    Int = int
    String = str  # noqa: PIE796
    Float = float
    Boolean = bool
    Date = date
    DateTime = datetime
    Time = time
    Decimal = Decimal

    @classmethod
    def from_type(cls, t: GraphQLScalarType) -> str:
        try:
            return cls[t.name].value.__name__
        except KeyError:
            return t.name


def joiner(func: Callable[T_ParamSpec, Iterator[str]]) -> Callable[T_ParamSpec, str]:
    """Join elements with a new line from an iterator."""

    @functools.wraps(func)
    def wrapper(*args: T_ParamSpec.args, **kwargs: T_ParamSpec.kwargs) -> str:
        return "\n".join(func(*args, **kwargs))

    return wrapper


@attrs.define
class Context:
    """Shared state during execution."""

    sync: bool = False
    """Sync or async client."""

    id_map: IDMap = attrs.Factory(dict)
    """Map to convert ids (custom scalars) to corresponding types."""

    remaining: set[str] = attrs.Factory(set)
    """Remaining type names that haven't been defined yet."""


# FIXME: break into class
@joiner
def generate(  # noqa: C901
    schema: GraphQLSchema,
    sync: bool = False,  # noqa: FBT001, FBT002
) -> Iterator[str]:
    """Code generation main function."""
    yield textwrap.dedent(
        """\
        # Code generated by dagger. DO NOT EDIT.

        from collections.abc import Sequence
        from typing import Optional

        from dagger.api.base import Arg, Enum, Input, Root, Scalar, Type, typecheck
        """,
    )

    # shared state between all handler instances
    ctx = Context(sync)

    handlers: tuple[Handler, ...] = (
        Scalar(ctx),
        Enum(ctx),
        Input(ctx),
        Object(ctx),
    )

    # collect object types for all id return types
    # used to replace custom scalars by objects in inputs
    for name, t in schema.type_map.items():
        if is_wrapping_type(t):
            t = t.of_type
        if not is_object_type(t):
            continue
        fields: dict[str, GraphQLField] = t.fields
        for field_name, f in fields.items():
            if field_name != "id":
                continue
            field_type = f.type
            if is_wrapping_type(field_type):
                field_type = field_type.of_type
            ctx.id_map[field_type.name] = name

    def sort_key(t: GraphQLNamedType) -> tuple[int, str]:
        for i, handler in enumerate(handlers):
            if handler.predicate(t):
                return i, t.name
        return -1, t.name

    def group_key(t: GraphQLNamedType) -> Handler | None:
        for handler in handlers:
            if handler.predicate(t):
                return handler
        return None

    def type_name(t: GraphQLNamedType) -> str:
        if t.name.startswith("_") or (
            is_scalar_type(t) and not is_custom_scalar_type(t)
        ):
            return ""
        return t.name.replace("Query", "Client")

    all_types = sorted(schema.type_map.values(), key=sort_key)
    remaining = {type_name(t) for t in all_types}
    ctx.remaining = {n for n in remaining if n}

    defined = []
    for handler, types in groupby(all_types, group_key):
        for t in types:
            name = type_name(t)
            if not handler or not name:
                ctx.remaining.discard(name)
                continue
            yield handler.render(t)
            defined.append(name)
            ctx.remaining.discard(name)

    yield ""
    yield "__all__ = ["
    yield from (indent(f'"{name}",') for name in defined)
    yield "]"


# FIXME: these typeguards should be contributed upstream
#        https://github.com/graphql-python/graphql-core/issues/183


def is_required_type(t: GraphQLType) -> TypeGuard[GraphQLNonNull]:
    return isinstance(t, GraphQLNonNull)


def is_list_type(t: GraphQLType) -> TypeGuard[GraphQLList]:
    return isinstance(t, GraphQLList)


def is_wrapping_type(t: GraphQLType) -> TypeGuard[GraphQLWrappingType]:
    return isinstance(t, GraphQLWrappingType)


def is_scalar_type(t: GraphQLType) -> TypeGuard[GraphQLScalarType]:
    return isinstance(t, GraphQLScalarType)


def is_input_object_type(t: GraphQLType) -> TypeGuard[GraphQLInputObjectType]:
    return isinstance(t, GraphQLInputObjectType)


def is_object_type(t: GraphQLType) -> TypeGuard[GraphQLObjectType]:
    return isinstance(t, GraphQLObjectType)


def is_output_leaf_type(t: GraphQLOutputType) -> TypeGuard[GraphQLLeafType]:
    return is_leaf_type(get_named_type(t))


def is_custom_scalar_type(t: GraphQLNamedType) -> TypeGuard[GraphQLScalarType]:
    t = get_named_type(t)
    return is_scalar_type(t) and t.name not in Scalars.__members__


def is_enum_type(t: GraphQLNamedType) -> TypeGuard[GraphQLEnumType]:
    return isinstance(t, GraphQLEnumType)


def format_name(s: str) -> str:
    # rewrite acronyms, initialisms and abbreviations
    s = ACRONYM_RE.sub(lambda m: m.group(0).title(), s)
    s = camel_to_snake(s)
    if iskeyword(s):
        s += "_"
    return s


def format_input_type(t: GraphQLInputType, id_map: IDMap) -> str:
    """May be used in an input object field or an object field parameter."""
    if is_required_type(t):
        t = t.of_type
        fmt = "%s"
    else:
        fmt = "Optional[%s]"

    if is_list_type(t):
        return fmt % f"list[{format_input_type(t.of_type, id_map)}]"

    if is_custom_scalar_type(t) and t.name in id_map:
        return fmt % id_map[t.name]

    return fmt % (Scalars.from_type(t) if is_scalar_type(t) else t.name)


def format_output_type(t: GraphQLOutputType) -> str:
    """May be used as the output type of an object field."""
    # only wrap optional and list when ready
    if is_output_leaf_type(t):
        return format_input_type(t, {})

    # when building the query return shouldn't be None
    # even if optional to not break the chain while
    # we're only building the query
    # FIXME: detect this when returning the scalar
    #        since it affects the result
    if is_wrapping_type(t):
        return format_output_type(t.of_type)

    return Scalars.from_type(t) if is_scalar_type(t) else t.name


def output_type_description(t: GraphQLOutputType) -> str:
    if is_wrapping_type(t):
        return output_type_description(t.of_type)
    if isinstance(t, GraphQLNamedType) and t.description:
        return t.description
    return ""


def doc(s: str) -> str:
    """Wrap string in docstring quotes."""
    if "\n" in s:
        s = f"{s}\n"
    return f'"""{s}"""'


class _InputField:
    """Input object field or object field argument."""

    def __init__(
        self,
        ctx: Context,
        name: str,
        graphql: GraphQLInputField | GraphQLArgument,
        parent: "_ObjectField| None" = None,
    ) -> None:
        self.ctx = ctx
        self.graphql_name = name
        self.graphql = graphql

        self.name = format_name(name)
        self.named_type = get_named_type(graphql.type)

        # On object type fields, don't replace ID scalar with object
        # only if field name is `id` and the corresponding type is different
        # from the output type (e.g., `file(id: FileID) -> File`, but also
        # `with_rootfs(id: Directory) -> Container`).
        id_map = ctx.id_map
        if (
            name == "id"
            and is_custom_scalar_type(graphql.type)
            and self.named_type.name in id_map
            and parent
            and get_named_type(parent.graphql.type).name == id_map[self.named_type.name]
        ):
            id_map = {}

        self.type = format_input_type(graphql.type, id_map)
        self.description = graphql.description
        self.has_default = graphql.default_value is not Undefined
        self.default_value = graphql.default_value

        if not is_required_type(graphql.type) and not self.has_default:
            self.default_value = None
            self.has_default = True

    @joiner
    def __str__(self) -> Iterator[str]:
        """Output for an InputObject field."""
        yield ""
        yield self.as_param()
        if self.description:
            yield doc(self.description)

    def as_param(self) -> str:
        """As a parameter in a function signature."""
        # broaden list types to Sequence on field inputs
        typ = re.sub(r"list\[", "Sequence[", self.type)
        out = f"{self.name}: {typ}"
        if self.has_default:
            # repr uses single quotes for strings, contrary to black
            val = repr(self.default_value).replace("'", '"')
            out = f"{out} = {val}"
        return out

    @joiner
    def as_doc(self) -> Iterator[str]:
        """As a part of a docstring."""
        yield f"{self.name}:"
        if self.description:
            for line in self.description.split("\n"):
                yield from wrap_indent(line)

    def as_arg(self) -> str:
        """As a Arg object for the query builder."""
        params = [f'"{self.graphql_name}"', self.name]
        if self.has_default:
            # repr uses single quotes for strings, contrary to black
            params.append(repr(self.default_value).replace("'", '"'))
        return f"Arg({', '.join(params)}),"


class _ObjectField:
    """Field of an object type."""

    def __init__(
        self,
        ctx: Context,
        name: str,
        field: GraphQLField,
    ) -> None:
        self.ctx = ctx
        self.graphql_name = name
        self.graphql = field

        self.name = format_name(name)
        self.args = sorted(
            (_InputField(ctx, *args, parent=self) for args in field.args.items()),
            key=attrgetter("has_default"),
        )
        self.description = field.description
        self.is_leaf = is_output_leaf_type(field.type)
        self.is_custom_scalar = is_custom_scalar_type(field.type)
        self.type = format_output_type(field.type).replace("Query", "Client")

    @joiner
    def __str__(self) -> Iterator[str]:
        yield from (
            "",
            "@typecheck",
            self.func_signature(),
            indent(self.func_body()),
        )

    def func_signature(self) -> str:
        params = ", ".join(chain(("self",), (a.as_param() for a in self.args)))
        # arbitrary heuristic to force trailing comma in long signatures
        if len(params) > 40:  # noqa: PLR2004
            params = f"{params},"
        prefix = "" if self.ctx.sync or not self.is_leaf else "async "
        sig = f"{prefix}def {self.name}({params}) -> {self.type}:"
        if self.ctx.remaining:
            sig = re.sub(rf"\b({'|'.join(self.ctx.remaining)})\b", r'"\1"', sig)
        return sig

    @joiner
    def func_body(self) -> Iterator[str]:
        if docstring := self.func_doc():
            yield doc(docstring)

        if not self.args:
            yield "_args: list[Arg] = []"
        else:
            yield "_args = ["
            yield from (indent(arg.as_arg()) for arg in self.args)
            yield "]"

        yield f'_ctx = self._select("{self.graphql_name}", _args)'

        if self.is_leaf:
            if self.ctx.sync:
                yield f"return _ctx.execute_sync({self.type})"
            else:
                yield f"return await _ctx.execute({self.type})"
        else:
            yield f"return {self.type}(_ctx)"

    def func_doc(self) -> str:
        def _out():
            if self.description:
                for line in self.description.split("\n"):
                    yield wrap(line)

            if deprecated := self.deprecated():
                yield chain(
                    (".. deprecated::",),
                    wrap_indent(deprecated),
                )

            if self.name == "id":
                yield (
                    "Note",
                    "----",
                    "This is lazyly evaluated, no operation is actually run.",
                )

            if any(arg.description for arg in self.args):
                yield chain(
                    (
                        "Parameters",
                        "----------",
                    ),
                    (arg.as_doc() for arg in self.args),
                )

            if self.is_leaf and (
                return_doc := output_type_description(self.graphql.type)
            ):
                yield chain(
                    (
                        "Returns",
                        "-------",
                        self.type,
                    ),
                    wrap_indent(return_doc),
                )

        return "\n\n".join("\n".join(section) for section in _out())

    def deprecated(self) -> str:
        def _format_name(m):
            name = format_name(m.group().strip("`"))
            return f":py:meth:`{name}`"

        return (
            DEPRECATION_RE.sub(_format_name, reason)
            if (reason := self.graphql.deprecation_reason)
            else ""
        )


_H = TypeVar("_H", bound=GraphQLNamedType)
"""Handler generic type"""


class Predicate(Protocol):
    def __call__(self, _: Any) -> bool:
        ...


@attrs.define
class Handler(ABC, Generic[_H]):
    ctx: Context
    """Generation execution context."""

    predicate: ClassVar[Predicate] = staticmethod(lambda _: False)
    """Does this handler render the given type?"""

    @joiner
    def render(self, t: _H) -> Iterator[str]:
        yield ""
        yield self.render_head(t)
        yield indent(self.render_body(t))
        yield ""

    def render_head(self, t: _H) -> str:
        return f"class {t.name}(Type):"

    @joiner
    def render_body(self, t: _H) -> Iterator[str]:
        if t.description:
            yield from wrap(doc(t.description))


@attrs.define
class Scalar(Handler[GraphQLScalarType]):
    predicate: ClassVar[Predicate] = staticmethod(is_custom_scalar_type)

    def render_head(self, t: GraphQLScalarType) -> str:
        return super().render_head(t).replace("Type", "Scalar")

    def render_body(self, t: GraphQLScalarType) -> str:
        return super().render_body(t) or "..."


@attrs.define
class Enum(Handler[GraphQLEnumType]):
    predicate: ClassVar[Predicate] = staticmethod(is_enum_type)

    def render_head(self, t: GraphQLEnumType) -> str:
        return super().render_head(t).replace("Type", "Enum")

    @joiner
    def render_body(self, t: GraphQLEnumType) -> Iterator[str]:
        if body := super().render_body(t):
            yield body

        for name, value in sorted(t.values.items()):
            yield ""

            # repr uses single quotes for strings, contrary to black
            val = repr(value.value).replace("'", '"')
            yield f"{name} = {val}"

            if value.description:
                yield doc(value.description)


class Field(Protocol):
    name: str
    graphql_name: str

    def __str__(self) -> str:
        ...


_O = TypeVar("_O", GraphQLInputObjectType, GraphQLObjectType)
"""Object handler generic type"""

_F: TypeAlias = _InputField | _ObjectField


class ObjectHandler(Handler[_O]):
    @abstractmethod
    def fields(self, t: _O) -> Iterator[_F]:
        ...

    @joiner
    def render_body(self, t: _O) -> Iterator[str]:
        if body := super().render_body(t):
            yield body
        yield from (
            str(field)
            # sorting by graphql name rather than pytnon name for
            # consistency with other SDKs
            for field in sorted(self.fields(t), key=attrgetter("graphql_name"))
        )


class Input(ObjectHandler[GraphQLInputObjectType]):
    predicate: ClassVar[Predicate] = staticmethod(is_input_object_type)

    def render_head(self, t: GraphQLInputObjectType) -> str:
        return super().render_head(t).replace("Type", "Input")

    def fields(self, t: GraphQLInputObjectType) -> Iterator[_InputField]:
        return (
            _InputField(self.ctx, *args)
            for args in cast(GraphQLInputFieldMap, t.fields).items()
        )


class Object(ObjectHandler[GraphQLObjectType]):
    predicate: ClassVar[Predicate] = staticmethod(is_object_type)

    def render_head(self, t: GraphQLObjectType) -> str:
        return super().render_head(t).replace("Query(Type)", "Client(Root)")

    def fields(self, t: GraphQLObjectType) -> Iterator[_ObjectField]:
        return (
            _ObjectField(self.ctx, *args)
            for args in cast(GraphQLFieldMap, t.fields).items()
        )
