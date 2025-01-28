import enum
import functools
import itertools
import logging
import re
import textwrap
from abc import ABC, abstractmethod
from collections.abc import Callable, Container, Iterable, Iterator
from dataclasses import dataclass, field
from datetime import date, datetime, time
from decimal import Decimal
from functools import partial
from itertools import chain, groupby
from keyword import iskeyword
from operator import itemgetter
from typing import (
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
)
from graphql.pyutils import camel_to_snake
from graphql.type.schema import TypeMap

ACRONYM_RE = re.compile(r"([A-Z\d]+)(?=[A-Z\d]|$)")
"""Pattern for grouping initialisms."""

DEPRECATION_RE = re.compile(r"`([a-zA-Z\d_]+)`")
"""Pattern for extracting replaced references in deprecations."""

logger = logging.getLogger(__name__)

indent = partial(textwrap.indent, prefix=" " * 4)
wrap = textwrap.wrap
wrap_indent = partial(wrap, initial_indent=" " * 4, subsequent_indent=" " * 4)


T_ParamSpec = ParamSpec("T_ParamSpec")

# These alias types are used to make the code more self-documenting.
IDName: TypeAlias = str
TypeName: TypeAlias = str
FieldName: TypeAlias = str
PythonName: TypeAlias = str
OutputTypeFormat: TypeAlias = str

IDSet: TypeAlias = frozenset[IDName]


def joiner(func: Callable[T_ParamSpec, Iterable[str]]) -> Callable[T_ParamSpec, str]:
    """Join elements with a new line from an iterator."""

    @functools.wraps(func)
    def wrapper(*args: T_ParamSpec.args, **kwargs: T_ParamSpec.kwargs) -> str:
        return "\n".join(func(*args, **kwargs))

    return wrapper


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


@dataclass
class Context:
    """Shared state during execution."""

    ids: frozenset[IDName] = field(default_factory=frozenset)
    """Set of ID scalar names."""

    defined: set[str] = field(default_factory=set)
    """Types that have already been defined."""

    remaining: set[str] = field(default_factory=set)
    """Remaining type names that haven't been defined yet."""

    def process_type(self, name: str):
        # This is only needed to keep track of remaining types because
        # of forward references.
        self.remaining.remove(name)
        self.defined.add(name)

    def render_types(self, s: str) -> str:
        """Render type names as forward references if they haven't been defined yet."""
        if not self.remaining:
            return s

        # Add quotes to names that haven't been defined yet (forward references).
        # Need to fix optionals because `"File" | None` is not a valid annotation.
        # The whole annotation needs to be quoted (`"File | None"`).
        s = re.sub(rf"\b({'|'.join(self.remaining)})\b", r'"\1"', s).replace(
            '" | None',
            ' | None"',
        )
        return re.sub(
            rf'list\["({"|".join(self.remaining)})"\] \| None', r'"list[\1] | None"', s
        )


_H = TypeVar("_H", bound=GraphQLNamedType)
"""Handler generic type"""


Predicate: TypeAlias = Callable[..., bool]


@dataclass
class Handler(ABC, Generic[_H]):
    ctx: Context
    """Generation execution context."""

    predicate: ClassVar[Predicate] = staticmethod(lambda _: False)
    """Does this handler render the given type?"""

    def supertype_name(self, t: _H) -> str:
        return self.__class__.__name__

    def type_name(self, t: _H) -> str:
        return t.name

    @joiner
    def render(self, t: _H) -> Iterator[str]:
        yield ""
        yield self.render_head(t)
        yield indent(self.render_body(t))
        yield ""

    def render_head(self, t: _H) -> str:
        return f"class {self.type_name(t)}({self.supertype_name(t)}):"

    @joiner
    def render_body(self, t: _H) -> Iterator[str]:
        if t.description:
            yield from wrap(doc(t.description))


@joiner
def generate(schema: GraphQLSchema) -> Iterator[str]:
    """Code generation main function."""
    yield textwrap.dedent(
        """\
        # Code generated by dagger. DO NOT EDIT.

        import warnings  # noqa: F401
        from collections.abc import Callable
        from dataclasses import dataclass
        import opentelemetry.trace
        import opentelemetry.context
        from opentelemetry.trace.span import TraceState

        from typing_extensions import Self

        from dagger.client._core import Arg, Root
        from dagger.client._guards import typecheck
        from dagger.client.base import Enum, Input, Scalar, Type
        """,
    )

    # Pre-create handy maps to make handler code simpler.
    ids = frozenset(n for n, t in schema.type_map.items() if is_id_type(t))

    # shared state between all handler instances
    ctx = Context(ids=ids)

    handlers: tuple[Handler, ...] = (
        Scalar(ctx),
        Enum(ctx),
        Input(ctx),
        Object(ctx),
    )

    # Split into two iterators to update ctx.remaining.
    types_n, types_g = itertools.tee(get_grouped_types(handlers, schema.type_map))

    # Track types that haven't been defined yet, to format as a forward reference.
    ctx.remaining.update(name for _, name, _ in types_n)

    for handler, type_name, named_type in types_g:
        yield handler.render(named_type)
        ctx.process_type(type_name)

    yield ""
    yield "dag = Client()"
    yield '"""The global client instance."""'
    ctx.defined.add("dag")

    yield ""
    yield "__all__ = ["
    yield from (indent(f"{quote(name)},") for name in sorted(ctx.defined))
    yield "]"


def get_grouped_types(handlers: tuple[Handler, ...], type_map: TypeMap):
    """Group types by handler and sorted by their name."""

    def _filtered():
        for n, t in type_map.items():
            if n.startswith("_") or is_builtin_scalar_type(t):
                continue
            for i, handler in enumerate(handlers):
                if handler.predicate(t):
                    yield i, n

    for _, items in groupby(sorted(_filtered()), itemgetter(0)):
        for index, name in items:
            named_type = type_map[name]
            handler = handlers[index]
            formatted_name = handler.type_name(named_type)
            yield handler, formatted_name, named_type


# TODO: these typeguards should be contributed upstream
#        https://github.com/graphql-python/graphql-core/issues/183


def is_required_type(t: GraphQLType) -> TypeGuard[GraphQLNonNull]:
    return isinstance(t, GraphQLNonNull)


def is_list_type(t: GraphQLType) -> TypeGuard[GraphQLList]:
    if is_required_type(t):
        t = t.of_type
    return isinstance(t, GraphQLList)


def is_list_of_objects_type(
    t: GraphQLType,
) -> TypeGuard[GraphQLList[GraphQLObjectType]]:
    return is_list_type(t) and is_object_type(get_named_type(t))


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


def is_custom_scalar_type(t: GraphQLType) -> TypeGuard[GraphQLScalarType]:
    t = get_named_type(t)
    return is_scalar_type(t) and t.name not in Scalars.__members__


def is_builtin_scalar_type(t: GraphQLNamedType) -> TypeGuard[GraphQLScalarType]:
    return is_scalar_type(t) and not is_custom_scalar_type(t)


def is_enum_type(t: GraphQLNamedType) -> TypeGuard[GraphQLEnumType]:
    return isinstance(t, GraphQLEnumType)


def is_self_chainable(t: GraphQLObjectType) -> bool:
    """Checks if an object type has any fields that return that same type."""
    return any(
        f
        for f in t.fields.values()
        # Only consider fields that return a non-null object.
        if is_required_type(f.type)
        and is_object_type(f.type.of_type)
        and f.type.of_type.name == t.name
    )


def is_id_type(
    t: GraphQLType,
    known_ids: Container[IDName] | None = None,
) -> TypeGuard[GraphQLScalarType]:
    t = get_named_type(t)
    if not is_scalar_type(t):
        return False
    return t.name in known_ids if known_ids else t.name.endswith("ID")


def type_from_id(t: GraphQLType) -> TypeName | None:
    """Return the type name for the given id type name."""
    return t.name.removesuffix("ID") if is_id_type(t) else None


def id_from_type(t: GraphQLType) -> IDName | None:
    """Return the id type name for the given type name."""
    return f"{t.name}ID" if is_id_type(t) else None


def id_query_field(t: GraphQLType) -> FieldName | None:
    """Get the field name under Query that returns the given id type."""
    type_name = type_from_id(t)
    return f"load{type_name}FromID" if type_name else None


def format_name(s: str) -> str:
    """Format a GraphQL field or argument name into Python."""
    # rewrite acronyms, initialisms and abbreviations
    s = ACRONYM_RE.sub(lambda m: m.group(0).title(), s)
    s = camel_to_snake(s)
    if iskeyword(s):
        s += "_"
    return s


def format_input_type(t: GraphQLInputType, convert_id=True) -> str:
    """May be used in an input object field or an object field parameter."""
    if is_required_type(t):
        t = t.of_type
        fmt = "%s"
    else:
        fmt = "%s | None"

    if is_list_type(t):
        return fmt % f"list[{format_input_type(t.of_type, convert_id)}]"

    if convert_id and is_id_type(t):
        return fmt % type_from_id(t)

    return fmt % (Scalars.from_type(t) if is_scalar_type(t) else get_named_type(t).name)


def format_output_type(t: GraphQLOutputType) -> str:
    """May be used as the output type of an object field."""
    # When returning objects we're in query building mode, so don't return
    # None even if the field's return is optional.
    if not is_output_leaf_type(t) and not is_required_type(t):
        t = GraphQLNonNull(t)
    return format_input_type(t, False)


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
    elif s.endswith('"'):
        s += " "
    return f'"""{s}"""'


def quote(s: str) -> str:
    """Wrap string in quotes."""
    return f'"{s}"'


class _InputField:
    """Input object field or object field argument."""

    def __init__(
        self,
        ctx: Context,
        name: str,
        graphql: GraphQLInputField | GraphQLArgument,
        parent: "_ObjectField | None" = None,
    ) -> None:
        self.ctx = ctx
        self.graphql_name = name
        self.graphql = graphql

        self.name = format_name(name)
        self.named_type = get_named_type(graphql.type)
        self.parent_return_type: TypeName | None = (
            get_named_type(parent.graphql.type).name if parent else None
        )
        self.parent_object_name: TypeName | None = (
            parent.parent_name if parent else None
        )

        # On object type fields, don't replace ID scalar with object
        # only if field name is `id` and the corresponding type is different
        # from the output type (e.g., `file(id: FileID) -> File`).
        convert_id = not (
            name == "id" and self.parent_return_type == type_from_id(self.named_type)
        )

        self.type = format_input_type(graphql.type, convert_id)
        self.is_self = self.type == self.parent_object_name
        self.description = graphql.description
        self.has_default = graphql.default_value is not Undefined

        default_value = graphql.default_value
        self.default_is_mutable = isinstance(default_value, list)
        if self.default_is_mutable:
            default_value = ()

        if not is_required_type(graphql.type) and not self.has_default:
            default_value = None
            self.has_default = True

        if default_value and is_enum_type(self.named_type):
            self.default_value = f"{self.named_type.name}.{default_value}"
        else:
            # repr uses single quotes for strings, contrary to black
            self.default_value = repr(default_value).replace("'", '"')

    @joiner
    def __str__(self) -> Iterator[str]:
        """Output for an InputObject field."""
        yield ""
        yield self.ctx.render_types(self.as_param())

        if self.description:
            yield doc(self.description)

    def as_param(self) -> str:
        """As a parameter in a function signature."""
        type_ = "Self" if self.is_self else self.type
        out = f"{self.name}: {type_}"
        if self.default_is_mutable:
            if not out.endswith("| None"):
                out = f"{out} | None"
            out = f"{out} = None"
        elif self.has_default:
            out = f"{out} = {self.default_value}"
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
        params = [quote(self.graphql_name), self.name]
        if self.default_is_mutable:
            params[1] = f"{self.default_value} if {self.name} is None else {self.name}"
        if self.has_default:
            params.append(self.default_value)
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
        self.parent_name = get_named_type(parent).name

        self.required_args = []
        self.default_args = []
        for args in field.args.items():
            arg = _InputField(ctx, *args, parent=self)
            (self.default_args if arg.has_default else self.required_args).append(arg)
        self.args = self.required_args + self.default_args
        self.description = field.description

        self.is_leaf = is_output_leaf_type(field.type)
        self.is_list = is_list_of_objects_type(field.type)
        self.is_exec = self.is_leaf or self.is_list
        self.is_void = self.is_leaf and self.named_type.name == "Void"
        self.is_sync = self.is_leaf and self.name == "sync"
        self.type = format_output_type(field.type).replace("Query", "Client")

        # Currently, `sync` is the only field where the error is all we
        # care about but more could be added later.
        # To avoid wasting a result, we return the ID which is a leaf value
        # and triggers execution, but then convert to object in the SDK to
        # allow continued chaining.
        self.convert_id = False
        if name != "id" and is_id_type(field.type) and self.is_leaf:
            converted = type_from_id(self.named_type)
            if self.parent_name == converted:
                self.type = converted
                self.convert_id = True

        self.id_query_field = id_query_field(self.named_type)

    @joiner
    def __str__(self) -> Iterator[str]:
        yield from (
            "",
            self.func_signature(),
            indent(self.func_body()),
        )

        # convenience to await any object that has a sync method
        # without having to call it explicitly
        if self.is_sync:
            yield from (
                "",
                "def __await__(self):",
                indent("return self.sync().__await__()"),
            )

    def func_signature(self) -> str:
        params = ", ".join(
            chain(
                ("self",),
                (a.as_param() for a in self.required_args),
                ("*",) if self.default_args else (),
                (a.as_param() for a in self.default_args),
            )
        )
        # arbitrary heuristic to force trailing comma in long signatures
        if len(params) > 40:  # noqa: PLR2004
            params = f"{params},"

        ret_type = "Self" if self.type == self.parent_name else self.type
        sig = self.ctx.render_types(f"def {self.name}({params}) -> {ret_type}:")
        if self.is_exec:
            sig = f"async {sig}"
        return sig

    @joiner
    def func_body(self) -> Iterator[str]:
        if docstring := self.func_doc():
            yield doc(docstring)

        if deprecated := self.deprecated():
            msg = f'Method "{self.name}" is deprecated: {deprecated}'.replace(
                '"', '\\"'
            )
            yield textwrap.dedent(
                f"""\
                warnings.warn(
                    "{msg}",
                    DeprecationWarning,
                    stacklevel=4,
                )\
                """
            )

        if not self.args:
            yield "_args: list[Arg] = []"
        else:
            yield "_args = ["
            yield from (indent(arg.as_arg()) for arg in self.args)
            yield "]"

        yield f'_ctx = self._select("{self.graphql_name}", _args)'

        if not self.is_exec:
            yield f"return {self.type}(_ctx)"
        elif self.convert_id:
            if _field := self.id_query_field:
                yield f"_id = await _ctx.execute({self.named_type.name})"
                yield (
                    f'_ctx = Client.from_context(_ctx)._select("{_field}",'
                    ' [Arg("id", _id)])'
                )
                yield f"return {self.type}(_ctx)"
            else:
                yield "await _ctx.execute()"
                yield "return self"
        elif self.is_list:
            _target = self.named_type.name
            yield f'_ctx = {_target}(_ctx)._select("id", [])'
            yield "@dataclass"
            yield "class Response:"
            yield f"    id: {self.named_type.name}ID"
            yield "_ids = await _ctx.execute(list[Response])"
            yield (
                f"return [{_target}(Client.from_context(_ctx)._select("
                f'"load{_target}FromID", [Arg("id", v.id)],)) for v in _ids]'
            )
        elif self.is_void:
            yield "await _ctx.execute()"
        else:
            yield f"return await _ctx.execute({self.type})"

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
                    "This is lazily evaluated, no operation is actually run.",
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


@dataclass
class Scalar(Handler[GraphQLScalarType]):
    predicate: ClassVar[Predicate] = staticmethod(is_custom_scalar_type)

    def render_body(self, t: GraphQLScalarType) -> str:
        return super().render_body(t) or "..."


@dataclass
class Enum(Handler[GraphQLEnumType]):
    predicate: ClassVar[Predicate] = staticmethod(is_enum_type)

    @joiner
    def render_body(self, t: GraphQLEnumType) -> Iterable[str]:
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

    def __str__(self) -> str: ...


_O = TypeVar("_O", GraphQLInputObjectType, GraphQLObjectType)
"""Object handler generic type"""

_F: TypeAlias = _InputField | _ObjectField


class ObjectHandler(Handler[_O]):
    @abstractmethod
    def fields(self, t: _O) -> Iterator[_F]: ...

    @joiner
    def render_body(self, t: _O) -> Iterator[str]:
        if body := super().render_body(t):
            yield body

        yield from (
            str(field)
            # Sorting by graphql name rather than python name for
            # consistency with other SDKs.
            for field in sorted(
                self.fields(t),
                key=lambda f: (getattr(f, "has_default", False), f.graphql_name),
            )
        )


class Input(ObjectHandler[GraphQLInputObjectType]):
    predicate: ClassVar[Predicate] = staticmethod(is_input_object_type)

    def render_head(self, t: GraphQLInputObjectType) -> str:
        return f"@typecheck\n@dataclass(slots=True)\n{super().render_head(t)}"

    def fields(self, t: GraphQLInputObjectType) -> Iterator[_InputField]:
        return (
            _InputField(self.ctx, *args)
            for args in cast(GraphQLInputFieldMap, t.fields).items()
        )


class Object(ObjectHandler[GraphQLObjectType]):
    predicate: ClassVar[Predicate] = staticmethod(is_object_type)

    def supertype_name(self, t: GraphQLObjectType) -> str:
        return "Root" if t.name == "Query" else "Type"

    def type_name(self, t: GraphQLObjectType) -> str:
        return super().type_name(t).replace("Query", "Client")

    def fields(self, t: GraphQLObjectType) -> Iterator[_ObjectField]:
        return (
            _ObjectField(self.ctx, *args, t)
            for args in cast(GraphQLFieldMap, t.fields).items()
        )

    def render_head(self, t: GraphQLObjectType) -> str:
        return f"@typecheck\n{super().render_head(t)}"

    @joiner
    def render_body(self, t: GraphQLObjectType) -> Iterator[str]:
        yield super().render_body(t)

        self_name = self.type_name(t)

        if self_name == "Span":
            yield textwrap.dedent(
                f'''
                def __init__(self, *args, **kwargs):
                    super().__init__(*args, **kwargs)
                    self.token = None

                async def __aenter__(self) -> "{self_name}":
                    # Fetch the actual span ID created by the engine
                    span_id_hex = (
                        await self._select("query", [])
                        .select("Query", "spanContext", [])
                        .select("SpanContext", "spanId", [])
                        .execute(str)
                    )
                    span_id = int(span_id_hex, 16)

                    # Get the current span context
                    current_span = opentelemetry.trace.get_current_span()
                    current_span_context = current_span.get_span_context()

                    # Extract trace ID and other fields from the current span context
                    trace_id = current_span_context.trace_id
                    trace_flags = current_span_context.trace_flags
                    trace_state = current_span_context.trace_state

                    # Construct the new SpanContext
                    new_span_context = opentelemetry.trace.SpanContext(
                        trace_id=trace_id,
                        span_id=span_id,
                        is_remote=True,
                        trace_flags=trace_flags,
                        trace_state=trace_state or TraceState(),
                    )

                    # Create a new context with the new SpanContext
                    new_context = opentelemetry.trace.set_span_in_context(opentelemetry.trace.NonRecordingSpan(new_span_context))

                    # Attach the new context and save the token for detachment
                    self.token = opentelemetry.context.attach(new_context)
                    return self

                async def __aexit__(self, exception_type, exception_value, exception_traceback) -> Void | None:
                    error: Error | None = None
                    if exception_type:
                        error = dag.error(f"{{exception_type.__name__}}: {{exception_value}}")
                    void = await self.end(error=error)
                    if self.token:
                        opentelemetry.context.detach(self.token)
                    return void
                '''  # noqa: E501
            )

        if is_self_chainable(t):
            yield textwrap.dedent(
                f'''
                def with_(self, cb: Callable[["{self_name}"], "{self_name}"]) -> "{self_name}":
                    """Call the provided callable with current {self_name}.

                    This is useful for reusability and readability by not breaking the calling chain.
                    """
                    return cb(self)
                '''  # noqa: E501
            )
