import enum
import functools
import logging
import re
import textwrap
from abc import ABC, abstractmethod
from collections.abc import Callable, Iterator
from dataclasses import dataclass, field
from datetime import date, datetime, time
from decimal import Decimal
from functools import partial
from itertools import chain, groupby
from keyword import iskeyword
from operator import attrgetter
from typing import (
    Any,
    ClassVar,
    Generic,
    ParamSpec,
    Protocol,
    TypeAlias,
    TypeGuard,
    TypeVar,
    cast,
)

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
    is_required_argument,
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
QueryFieldName: TypeAlias = str
IDMap: TypeAlias = dict[IDName, TypeName]
IDQueryMap: TypeAlias = dict[IDName, QueryFieldName]


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


@dataclass
class Context:
    """Shared state during execution."""

    sync: bool
    """Sync or async client."""

    id_map: IDMap
    """Map to convert ids (custom scalars) to corresponding types."""

    id_query_map: IDQueryMap
    """Map to convert types to ids."""

    remaining: set[str] = field(default_factory=set)
    """Remaining type names that haven't been defined yet."""


# TODO: break into class
@joiner
def generate(  # noqa: C901
    schema: GraphQLSchema,
    sync: bool = False,  # noqa: FBT001, FBT002
) -> Iterator[str]:
    """Code generation main function."""
    yield textwrap.dedent(
        """\
        # Code generated by dagger. DO NOT EDIT.

        import warnings
        from collections.abc import Sequence
        from dataclasses import dataclass
        from typing import Optional

        from dagger.api.base import Arg, Enum, Input, Root, Scalar, Type, typecheck
        """,
    )

    # Collect object types for all id return types.
    # Used to replace custom scalars by objects in inputs.
    id_map: IDMap = {}
    for type_name, t in schema.type_map.items():
        if not is_object_type(t):
            continue
        fields: dict[str, GraphQLField] = t.fields
        for field_name, f in fields.items():
            if field_name == "id":
                field_type = get_named_type(f.type)
                id_map[field_type.name] = type_name

    query_type = cast(GraphQLObjectType, schema.type_map["Query"])
    query_fields: dict[str, GraphQLField] = query_type.fields

    # Collect fields under Query that receive an id argument and return
    # an object type that also has an id field that returns the same
    # id type as the Query field argument.
    #
    # Example:
    #   `Query.directory(id: DirectoryID): Directory` matches
    #   `Directory.id(): DirectoryID`
    #
    # Used to create a classmethod that returns a Directory instance
    # from a DirectoryID by telling us which field to query for.
    id_query_map: IDQueryMap = {}
    for field_name, f in query_fields.items():
        field_type = get_named_type(f.type)
        id_arg = f.args.get("id")
        # Ignore fields that have required arguments other than id.
        if not id_arg or any(
            is_required_argument(arg)
            for arg_name, arg in f.args.items()
            if arg_name != "id"
        ):
            continue
        id_type = get_named_type(id_arg.type)
        if id_map.get(id_type.name) == field_type.name:
            id_query_map[id_type.name] = field_name

    # shared state between all handler instances
    ctx = Context(sync=sync, id_map=id_map, id_query_map=id_query_map)

    handlers: tuple[Handler, ...] = (
        Scalar(ctx),
        Enum(ctx),
        Input(ctx),
        Object(ctx),
    )

    def _sort_key(t: GraphQLNamedType) -> tuple[int, str]:
        for i, handler in enumerate(handlers):
            if handler.predicate(t):
                return i, t.name
        return -1, t.name

    def _group_key(t: GraphQLNamedType) -> Handler | None:
        for handler in handlers:
            if handler.predicate(t):
                return handler
        return None

    def _type_name(t: GraphQLNamedType) -> str:
        if t.name.startswith("_") or (
            is_scalar_type(t) and not is_custom_scalar_type(t)
        ):
            return ""
        return t.name.replace("Query", "Client")

    all_types = sorted(schema.type_map.values(), key=_sort_key)
    remaining = {_type_name(t) for t in all_types}
    ctx.remaining = {n for n in remaining if n}

    defined = []
    for handler, types in groupby(all_types, _group_key):
        for t in types:
            type_name = _type_name(t)
            if not handler or not type_name:
                ctx.remaining.discard(type_name)
                continue
            yield handler.render(t)
            defined.append(type_name)
            ctx.remaining.discard(type_name)

    yield ""
    yield "__all__ = ["
    yield from (indent(f'"{name}",') for name in defined)
    yield "]"


# TODO: these typeguards should be contributed upstream
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
    # TODO: detect this when returning the scalar
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
        parent: GraphQLObjectType,
    ) -> None:
        self.ctx = ctx
        self.graphql_name = name
        self.graphql = field

        self.name = format_name(name)
        self.named_type = get_named_type(field.type)
        self.args = sorted(
            (_InputField(ctx, *args, parent=self) for args in field.args.items()),
            key=attrgetter("has_default"),
        )
        self.description = field.description

        self.is_leaf = is_output_leaf_type(field.type)
        self.is_custom_scalar = is_custom_scalar_type(field.type)
        self.type = format_output_type(field.type).replace("Query", "Client")
        self.parent_name = get_named_type(parent).name
        self.convert_id = False

        # Currently, `sync` is the only field where the error is all we
        # care about but more could be added later.
        # To avoid wasting a result, we return the ID which is a leaf value
        # and triggers execution, but then convert to object in the SDK to
        # allow continued chaining.
        if (
            name != "id"
            and self.is_leaf
            and self.is_custom_scalar
            and self.named_type.name in ctx.id_map
        ):
            converted = ctx.id_map[self.named_type.name]
            if self.parent_name == converted:
                self.type = converted
                self.convert_id = True

        self.id_query_field = self.ctx.id_query_map.get(self.named_type.name)

    @joiner
    def __str__(self) -> Iterator[str]:
        yield from (
            "",
            "@typecheck",
            self.func_signature(),
            indent(self.func_body()),
        )

        # convenience to await any object that has a sync method
        # without having to call it explicitly
        if not self.ctx.sync and self.is_leaf and self.name == "sync":
            yield from (
                "",
                "def __await__(self):",
                indent("return self.sync().__await__()"),
            )

        if self.name == "id":
            yield from (
                "",
                "@classmethod",
                "def _id_type(cls) -> type[Scalar]:",
                indent(f"return {self.type}"),
            )
            if self.id_query_field:
                yield from (
                    "",
                    "@classmethod",
                    "def _from_id_query_field(cls):",
                    indent(f'return "{self.id_query_field}"'),
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

        if deprecated := self.deprecated():
            msg = f'Method "{self.name}" is deprecated: {deprecated}'.replace(
                '"', '\\"'
            )
            yield textwrap.dedent(f"""\
                warnings.warn(
                    "{msg}",
                    DeprecationWarning,
                    stacklevel=4,
                )\
                """)

        if not self.args:
            yield "_args: list[Arg] = []"
        else:
            yield "_args = ["
            yield from (indent(arg.as_arg()) for arg in self.args)
            yield "]"

        yield f'_ctx = self._select("{self.graphql_name}", _args)'

        if self.is_leaf:
            exec_ = "_ctx.execute_sync" if self.ctx.sync else "await _ctx.execute"
            if self.convert_id:
                if _field := self.id_query_field:
                    yield f"_id = {exec_}({self.named_type.name})"
                    yield f'_ctx = self._root_select("{_field}", [Arg("id", _id)])'
                    yield f"return {self.type}(_ctx)"
                else:
                    yield f"{exec_}()"
                    yield "return self"
            else:
                yield f"return {exec_}({self.type})"
        else:
            yield f"return {self.type}(_ctx)"

    def func_doc(self) -> str:
        def _out():
            if self.description:
                yield (textwrap.fill(line) for line in self.description.splitlines())

            if deprecated := self.deprecated(":py:meth:`", "`"):
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

            if self.is_leaf:
                return_doc = output_type_description(self.graphql.type)
                if not self.convert_id and return_doc:
                    yield chain(
                        (
                            "Returns",
                            "-------",
                            self.type,
                        ),
                        wrap_indent(return_doc),
                    )

                yield chain(
                    (
                        "Raises",
                        "------",
                        "ExecuteTimeoutError",
                    ),
                    wrap_indent(
                        "If the time to execute the query exceeds the "
                        "configured timeout."
                    ),
                    (
                        "QueryError",
                        indent("If the API returns an error."),
                    ),
                )

        return "\n\n".join("\n".join(section) for section in _out())

    def deprecated(self, prefix='"', suffix='"') -> str:
        def _format_name(m):
            name = format_name(m.group().strip("`"))
            return f"{prefix}{name}{suffix}"

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


@dataclass
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


@dataclass
class Scalar(Handler[GraphQLScalarType]):
    predicate: ClassVar[Predicate] = staticmethod(is_custom_scalar_type)

    def render_head(self, t: GraphQLScalarType) -> str:
        return super().render_head(t).replace("Type", "Scalar")

    def render_body(self, t: GraphQLScalarType) -> str:
        return super().render_body(t) or "..."


@dataclass
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
        return f"@dataclass\nclass {t.name}(Input):"

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
            _ObjectField(self.ctx, *args, t)
            for args in cast(GraphQLFieldMap, t.fields).items()
        )
