from textwrap import dedent

import graphql
import pytest
from graphql import (
    GraphQLArgument as Argument,
)
from graphql import (
    GraphQLBoolean as Boolean,
)
from graphql import (
    GraphQLEnumType,
    GraphQLEnumValue,
    GraphQLID,
)
from graphql import (
    GraphQLField as Field,
)
from graphql import (
    GraphQLInputField as Input,
)
from graphql import (
    GraphQLInputField as InputField,
)
from graphql import (
    GraphQLInputObjectType as InputObject,
)
from graphql import (
    GraphQLInt as Int,
)
from graphql import (
    GraphQLInterfaceType as Interface,
)
from graphql import (
    GraphQLList as List,
)
from graphql import (
    GraphQLNonNull as NonNull,
)
from graphql import (
    GraphQLObjectType as Object,
)
from graphql import (
    GraphQLScalarType as Scalar,
)
from graphql import (
    GraphQLString as String,
)

from codegen.generator import (
    Context,
    _InputField,
    _ObjectField,
    doc,
    format_input_type,
    format_name,
    format_output_type,
)
from codegen.generator import (
    Enum as EnumHandler,
)
from codegen.generator import (
    Input as InputHandler,
)
from codegen.generator import (
    Scalar as ScalarHandler,
)


@pytest.fixture
def ctx():
    return Context(
        ids=frozenset({}),
        remaining={"Secret"},
    )


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        ("stdout", "stdout"),
        ("envVariable", "env_variable"),  # casing
        ("from", "from_"),  # reserved keyword
        ("str", "str_"),  # builtin
        ("withFS", "with_fs"),  # initialism
    ],
)
def test_format_name(graphql, expected):
    assert format_name(graphql) == expected


opts = InputObject(
    "Options",
    fields={
        "key": InputField(NonNull(Scalar("CacheVolumeID"))),
        "name": InputField(String),
    },
)


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "list[str | None] | None"),
        (List(NonNull(String)), "list[str] | None"),
        (NonNull(Scalar("FileID")), "File"),
        (Scalar("FileID"), "File | None"),
        (NonNull(opts), "Options"),
        (opts, "Options | None"),
        (NonNull(List(NonNull(opts))), "list[Options]"),
        (NonNull(List(opts)), "list[Options | None]"),
        (List(NonNull(opts)), "list[Options] | None"),
        (List(opts), "list[Options | None] | None"),
    ],
)
def test_format_input_type(graphql, expected):
    assert format_input_type(graphql) == expected


cache_volume = Object(
    "CacheVolume",
    fields={
        "id": Field(
            NonNull(Scalar("CacheVolumeID")),
            {},
        ),
    },
)


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "list[str | None] | None"),
        (List(NonNull(String)), "list[str] | None"),
        (NonNull(Scalar("FileID")), "FileID"),
        (Scalar("FileID"), "FileID | None"),
        (NonNull(cache_volume), "CacheVolume"),
        (cache_volume, "CacheVolume"),
        (List(NonNull(cache_volume)), "list[CacheVolume]"),
        (List(cache_volume), "list[CacheVolume | None]"),
    ],
)
def test_format_output_type(graphql, expected):
    assert format_output_type(graphql) == expected


def _(type_: graphql.GraphQLInputType, default_value: str):
    """Read default value from JSON result the same way that graphql library does."""
    return type_, graphql.value_from_ast(graphql.parse_value(default_value), type_)


@pytest.mark.parametrize(
    ("name", "args", "expected"),
    [
        ("args", (NonNull(List(String)),), "args: list[str | None]"),
        ("secret", (NonNull(Scalar("SecretID")),), "secret: Secret"),
        ("secret", (Scalar("SecretID"),), "secret: Secret | None = None"),
        ("from", _(String, "null"), "from_: str | None = None"),
        ("lines", _(Int, "1"), "lines: int | None = 1"),
        (
            "configPath",
            _(NonNull(String), '"/dagger.json"'),
            "config_path: str = '/dagger.json'",
        ),
        # Go example: // +default="foo bar" -> "defaultValue": "\"foo bar\""
        ("space", _(String, '"foo bar"'), "space: str | None = 'foo bar'"),
        # Go example: // +default='foo bar' -> "defaultValue": "\"'foo bar'\""
        (
            "singleQuotes",
            _(String, "\"'foo bar'\""),
            "single_quotes: str | None = \"'foo bar'\"",
        ),
        # Go example: // +default=`foo bar` -> "defaultValue": "\"`foo bar`\""
        ("backticks", _(String, '"`foo bar`"'), "backticks: str | None = '`foo bar`'"),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_param(cls, name: str, args, expected: str, ctx: Context):
    assert _InputField(ctx, name, cls(*args)).as_param() == expected


@pytest.mark.parametrize(
    ("name", "args", "expected"),
    [
        (
            "context",
            (NonNull(Scalar("DirectoryID")),),
            'Arg("context", context),',
        ),
        (
            "secret",
            (Scalar("SecretID"),),
            'Arg("secret", secret, None),',
        ),
        (
            "lines",
            (Int, 1),
            'Arg("lines", lines, 1),',
        ),
        (
            "from",
            (String, None),
            'Arg("from", from_, None),',
        ),
        (
            "configPath",
            (NonNull(String), "/dagger.json"),
            "Arg(\"configPath\", config_path, '/dagger.json'),",
        ),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_arg(cls, name, args, expected, ctx: Context):
    assert _InputField(ctx, name, cls(*args)).as_arg() == expected


def test_input_object_field_deprecated():
    local_ctx = Context()
    input_type = InputObject(
        "LegacyInput",
        lambda: {
            "legacyField": InputField(
                String,
                description="Legacy config path.",
                deprecation_reason="Use `configPath` instead.",
            ),
            "active": InputField(Boolean),
        },
        description="Configuration options.",
    )

    rendered = InputHandler(local_ctx).render(input_type)

    assert "class LegacyInput(Input):" in rendered
    assert "legacy_field: str | None = None" in rendered
    assert ".. deprecated:: Use config_path instead." in rendered


def test_core_sync(ctx: Context):
    handler = _ObjectField(
        ctx,
        "sync",
        Field(NonNull(Scalar("FooID")), {}),
        Object("Foo", {}),
    )

    assert handler.func_signature() == "async def sync(self) -> Self:"

    assert str(handler.func_body()).endswith(
        'return await self._ctx.execute_sync(self, "sync", _args)'
    )


def test_user_sync_leaf(ctx: Context):
    handler = _ObjectField(
        ctx,
        "sync",
        Field(NonNull(String), {}),
        Object("Foo", {}),
    )

    assert handler.func_signature() == "async def sync(self) -> str:"

    assert str(handler.func_body()).endswith(
        dedent(
            """
            _args: list[Arg] = []
            _ctx = self._select("sync", _args)
            return await _ctx.execute(str)
            """.rstrip()
        )
    )


def test_user_sync_object(ctx: Context):
    handler = _ObjectField(
        ctx,
        "sync",
        Field(NonNull(Object("Foo", {})), {}),
        Object("Foo", {}),
    )
    assert str(handler) == dedent(
        """
        def sync(self) -> Self:
            _args: list[Arg] = []
            _ctx = self._select("sync", _args)
            return Foo(_ctx)
        """.rstrip()
    )


def test_func_doc_deprecated_args(ctx: Context):
    field = Field(
        String,
        {
            "path": Argument(NonNull(String)),
            "configDir": Argument(
                String,
                deprecation_reason="Use `configPath` instead.",
            ),
        },
        deprecation_reason="Use apply_config instead.",
    )

    parent = Object("Container", lambda: {"apply": field})
    handler = _ObjectField(ctx, "apply", field, parent)

    docstring = handler.func_doc()

    assert "Parameters" in docstring
    assert ".. deprecated::\n    Use apply_config instead." in docstring
    doc_lines = {line.strip() for line in docstring.splitlines()}
    assert "config_dir:" in doc_lines
    assert ".. deprecated:: Use config_path instead." in doc_lines

    body = handler.func_body()
    normalized = body.replace('\\"', '"')
    assert 'Method "apply" is deprecated: Use apply_config instead.' in normalized


def test_interface_methods_deprecated(ctx: Context):
    iface = Interface(
        "Fooer",
        lambda: {
            "foo": Field(
                String,
                {
                    "value": Argument(
                        Int,
                        deprecation_reason="Use `other` instead.",
                    )
                },
                deprecation_reason="Call `bar` instead.",
            ),
            "bar": Field(
                String,
                {"note": Argument(String, description="Caller note.")},
            ),
        },
    )

    foo_handler = _ObjectField(ctx, "foo", iface.fields["foo"], iface)
    foo_doc = foo_handler.func_doc()
    assert ".. deprecated::\n    Call :py:meth:`bar` instead." in foo_doc

    foo_doc_lines = {line.strip() for line in foo_doc.splitlines()}
    assert ".. deprecated:: Use other instead." in foo_doc_lines

    foo_body = foo_handler.func_body()
    foo_normalized = foo_body.replace('\\"', '"')
    assert 'Method "foo" is deprecated: Call "bar" instead.' in foo_normalized

    bar_handler = _ObjectField(ctx, "bar", iface.fields["bar"], iface)
    bar_doc = bar_handler.func_doc()
    assert ".. deprecated::" not in bar_doc
    assert "Parameters" in bar_doc

    bar_body = bar_handler.func_body()
    assert "warnings.warn" not in bar_body


@pytest.mark.parametrize(
    ("type_", "expected"),
    [
        (GraphQLID, False),
        (String, False),
        (Int, False),
        (Scalar("FileID"), True),
        (Object("Container", {}), False),
    ],
)
def test_scalar_predicate(type_, expected, ctx: Context):
    assert ScalarHandler(ctx).predicate(type_) is expected


@pytest.mark.parametrize(
    ("type_", "expected"),
    [
        # with doc
        (
            Scalar("SecretID", description="A unique identifier for a secret."),
            dedent(
                '''
                class SecretID(Scalar):
                    """A unique identifier for a secret."""
                ''',
            ),
        ),
        # without doc
        (
            Scalar("FileID"),
            dedent(
                """
                class FileID(Scalar):
                    ...
                """,
            ),
        ),
    ],
)
def test_scalar_render(type_, expected, ctx: Context):
    handler = ScalarHandler(ctx)
    assert handler.render(type_) == expected


@pytest.mark.parametrize(
    ("type_", "expected"),
    [
        # with doc
        (
            GraphQLEnumType(
                "Enumeration",
                {
                    "ONE": GraphQLEnumValue("ONE", description="First value."),
                    "TWO": GraphQLEnumValue("TWO", description="Second value."),
                    "THREE": GraphQLEnumValue("THREE", description="Third value."),
                },
                description="Example of an enumeration.",
            ),
            dedent(
                '''
                class Enumeration(Enum):
                    """Example of an enumeration."""

                    ONE = 'ONE'
                    """First value."""

                    THREE = 'THREE'
                    """Third value."""

                    TWO = 'TWO'
                    """Second value."""
                ''',
            ),
        ),
        # without doc
        (
            GraphQLEnumType(
                "Enumeration",
                {
                    "ONE": GraphQLEnumValue("ONE"),
                    "TWO": GraphQLEnumValue("TWO"),
                    "THREE": GraphQLEnumValue("THREE"),
                },
            ),
            dedent(
                """
                class Enumeration(Enum):

                    ONE = 'ONE'

                    THREE = 'THREE'

                    TWO = 'TWO'
                """,
            ),
        ),
        (
            GraphQLEnumType(
                "Mode",
                {
                    "VALUE": GraphQLEnumValue(
                        "VALUE",
                        deprecation_reason="Use ModeV2 instead.",
                    ),
                },
            ),
            dedent(
                '''
                class Mode(Enum):

                    VALUE = 'VALUE'
                    """.. deprecated:: Use ModeV2 instead."""
                ''',
            ),
        ),
    ],
)
def test_enum_render(type_, expected, ctx: Context):
    handler = EnumHandler(ctx)
    assert handler.render(type_) == expected


@pytest.mark.parametrize(
    ("original", "expected"),
    [
        (
            "Lorem ipsum dolores est.",
            '"""Lorem ipsum dolores est."""',
        ),
        (
            "Lorem ipsum dolores est.\n\nSecond paragraph.",
            dedent(
                '''\
                """Lorem ipsum dolores est.

                Second paragraph.
                """''',
            ),
        ),
        (
            '"Foo": bar.',
            r'""""Foo": bar."""',
        ),
        (
            'Example: "foobar"',
            r'"""Example: "foobar" """',
        ),
        (
            'Lorem ipsum dolores est.\n\nExample: "foobar"',
            dedent(
                '''\
                """Lorem ipsum dolores est.

                Example: "foobar"
                """''',
            ),
        ),
    ],
)
def test_doc(original: str, expected: str):
    assert doc(original) == expected
