from textwrap import dedent

import pytest
from graphql import GraphQLArgument as Argument
from graphql import GraphQLEnumType, GraphQLEnumValue, GraphQLID
from graphql import GraphQLField as Field
from graphql import GraphQLInputField as Input
from graphql import GraphQLInputField as InputField
from graphql import GraphQLInputObjectType as InputObject
from graphql import GraphQLInt as Int
from graphql import GraphQLList as List
from graphql import GraphQLNonNull as NonNull
from graphql import GraphQLObjectType as Object
from graphql import GraphQLScalarType as Scalar
from graphql import GraphQLString as String

from dagger._codegen.generator import (
    Context,
    _InputField,
    format_input_type,
    format_name,
    format_output_type,
)
from dagger._codegen.generator import Enum as EnumHandler
from dagger._codegen.generator import Scalar as ScalarHandler


@pytest.fixture()
def ctx():
    return Context(
        id_map={
            "CacheID": "CacheVolume",
            "FileID": "File",
            "SecretID": "Secret",
        },
        id_query_map={
            "ContainerID": "container",
            "DirectoryID": "directory",
        },
        simple_objects_map={},
        remaining={"Secret"},
    )


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        ("stdout", "stdout"),
        ("envVariable", "env_variable"),  # casing
        ("from", "from_"),  # reserved keyword
        ("type", "type"),  # builtin
        ("withFS", "with_fs"),  # initialism
    ],
)
def test_format_name(graphql, expected):
    assert format_name(graphql) == expected


opts = InputObject(
    "Options",
    fields={
        "key": InputField(NonNull(Scalar("CacheID"))),
        "name": InputField(String),
    },
)


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "Optional[list[Optional[str]]]"),
        (List(NonNull(String)), "Optional[list[str]]"),
        (NonNull(Scalar("FileID")), "File"),
        (Scalar("FileID"), "Optional[File]"),
        (NonNull(opts), "Options"),
        (opts, "Optional[Options]"),
        (NonNull(List(NonNull(opts))), "list[Options]"),
        (NonNull(List(opts)), "list[Optional[Options]]"),
        (List(NonNull(opts)), "Optional[list[Options]]"),
        (List(opts), "Optional[list[Optional[Options]]]"),
    ],
)
def test_format_input_type(graphql, expected, ctx: Context):
    assert format_input_type(graphql, ctx.id_map) == expected


cache_volume = Object(
    "CacheVolume",
    fields={
        "id": Field(
            NonNull(Scalar("CacheID")),
            {},
        ),
    },
)


@pytest.mark.parametrize(
    ("graphql", "expected"),
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "Optional[list[Optional[str]]]"),
        (List(NonNull(String)), "Optional[list[str]]"),
        (NonNull(Scalar("FileID")), "FileID"),
        (Scalar("FileID"), "Optional[FileID]"),
        (NonNull(cache_volume), "CacheVolume"),
        (cache_volume, "CacheVolume"),
        (List(NonNull(cache_volume)), "list[CacheVolume]"),
        (List(cache_volume), "list[Optional[CacheVolume]]"),
    ],
)
def test_format_output_type(graphql, expected):
    assert format_output_type(graphql) == expected


@pytest.mark.parametrize(
    ("name", "args", "expected"),
    [
        ("args", (NonNull(List(String)),), "args: Sequence[Optional[str]]"),
        ("secret", (NonNull(Scalar("SecretID")),), "secret: Secret"),
        ("secret", (Scalar("SecretID"),), "secret: Optional[Secret] = None"),
        ("from", (String, None), "from_: Optional[str] = None"),
        ("lines", (Int, 1), "lines: Optional[int] = 1"),
        (
            "configPath",
            (NonNull(String), "/dagger.json"),
            'config_path: str = "/dagger.json"',
        ),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_param(cls, name, args, expected, ctx: Context):
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
            'Arg("configPath", config_path, "/dagger.json"),',
        ),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_arg(cls, name, args, expected, ctx: Context):
    assert _InputField(ctx, name, cls(*args)).as_arg() == expected


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

                    ONE = "ONE"
                    """First value."""

                    THREE = "THREE"
                    """Third value."""

                    TWO = "TWO"
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

                    ONE = "ONE"

                    THREE = "THREE"

                    TWO = "TWO"
                """,
            ),
        ),
    ],
)
def test_enum_render(type_, expected, ctx: Context):
    handler = EnumHandler(ctx)
    assert handler.render(type_) == expected
