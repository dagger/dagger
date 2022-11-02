from textwrap import dedent

import pytest
from graphql import GraphQLArgument as Argument
from graphql import GraphQLField as Field
from graphql import GraphQLID as ID
from graphql import GraphQLInputField as Input
from graphql import GraphQLInputField as InputField
from graphql import GraphQLInputObjectType as InputObject
from graphql import GraphQLInt as Int
from graphql import GraphQLList as List
from graphql import GraphQLNonNull as NonNull
from graphql import GraphQLObjectType as Object
from graphql import GraphQLScalarType as Scalar
from graphql import GraphQLString as String

from dagger.client.codegen import Scalar as ScalarHandler
from dagger.client.codegen import _InputField, format_input_type, format_name, format_output_type


@pytest.mark.parametrize(
    "graphql, expected",
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
    "graphql, expected",
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "list[str | None] | None"),
        (List(NonNull(String)), "list[str] | None"),
        (NonNull(Scalar("FileID")), "FileID"),
        (Scalar("FileID"), "FileID | None"),
        (NonNull(opts), "Options"),
        (opts, "Options | None"),
        (NonNull(opts), "Options"),
        (NonNull(List(NonNull(opts))), "list[Options]"),
        (NonNull(List(opts)), "list[Options | None]"),
        (List(NonNull(opts)), "list[Options] | None"),
        (List(opts), "list[Options | None] | None"),
    ],
)
def test_format_input_type(graphql, expected):
    assert format_input_type(graphql) == expected


cache_volume = Object("CacheVolume", fields={"id": Field(NonNull(Scalar("CacheID")), {})})


@pytest.mark.parametrize(
    "graphql, expected",
    [
        (NonNull(List(NonNull(String))), "list[str]"),
        (List(String), "list[str | None] | None"),
        (List(NonNull(String)), "list[str] | None"),
        (NonNull(Scalar("FileID")), "FileID"),
        (Scalar("FileID"), "FileID | None"),
        (NonNull(cache_volume), "CacheVolume"),
        (cache_volume, "CacheVolume"),
        (List(NonNull(cache_volume)), "CacheVolume"),
        (List(cache_volume), "CacheVolume"),
    ],
)
def test_format_output_type(graphql, expected):
    assert format_output_type(graphql) == expected


@pytest.mark.parametrize(
    "name, args, expected",
    [
        ("context", (NonNull(Scalar("DirectoryID")),), "context: DirectoryID"),
        ("secret", (Scalar("SecretID"),), "secret: SecretID | None = None"),
        ("from", (String, None), "from_: str | None = None"),
        ("lines", (Int, 1), "lines: int | None = 1"),
        (
            "configPath",
            (NonNull(String), "/dagger.json"),
            "config_path: str = '/dagger.json'",
        ),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_param(cls, name, args, expected):
    assert _InputField(name, cls(*args)).as_param() == expected


@pytest.mark.parametrize(
    "name, args, expected",
    [
        ("context", (NonNull(Scalar("DirectoryID")),), "Arg('context', context),"),
        ("secret", (Scalar("SecretID"),), "Arg('secret', secret, None),"),
        ("lines", (Int, 1), "Arg('lines', lines, 1),"),
        ("from", (String, None), "Arg('from', from_, None),"),
        (
            "configPath",
            (NonNull(String), "/dagger.json"),
            "Arg('configPath', config_path, '/dagger.json'),",
        ),
    ],
)
@pytest.mark.parametrize("cls", [Argument, Input])
def test_input_field_arg(cls, name, args, expected):
    assert _InputField(name, cls(*args)).as_arg() == expected


@pytest.mark.parametrize(
    "type_, expected",
    [
        (ID, False),
        (String, False),
        (Int, False),
        (Scalar("FileID"), True),
        (Object("Container", {}), False),
    ],
)
def test_scalar_predicate(type_, expected):
    assert ScalarHandler().predicate(type_) is expected


@pytest.mark.parametrize(
    "type_, expected",
    [
        (Scalar("FileID"), 'FileID = NewType("FileID", str)\n'),
        (
            Scalar("SecretID", description="A unique identifier for a secret."),
            dedent(
                '''\
                SecretID = NewType("SecretID", str)
                """
                A unique identifier for a secret.
                """

                ''',
            ),
        ),
    ],
)
def test_scalar_render(type_, expected):
    handler = ScalarHandler()
    handler.render(type_)
    assert handler.output == [expected]
