import types
import typing

import pytest
from typing_extensions import Self

import dagger
from dagger import dag
from dagger._exceptions import QueryErrorValue
from dagger.client._core import Context
from dagger.mod import Module, _naming
from dagger.mod._converter import to_interface_impl
from dagger.mod._naming import SchemaNames, format_names, resolve_schema_names

pytestmark = [
    pytest.mark.anyio,
]

# How an engine at v1.0.0 or later formats the names below: acronyms are
# words of their own, which a plain case conversion can't tell.
ENGINE_NAMES = {
    "CAMEL": {
        "get_url": "getURL",
        "com_url": "comURL",
        "with_http_client": "withHTTPClient",
        "speak": "speak",
        "name": "name",
        "fetch": "fetch",
        "from": "from",
    },
    "SNAKE": {
        "Pond": "pond",
        "HttpFetcher": "http_fetcher",
        "PondSpeaker": "pond_speaker",
    },
    "PASCAL": {
        "HttpFetcher": "HTTPFetcher",
        "PondSpeaker": "PondSpeaker",
        "Pond_HttpFetcher": "PondHTTPFetcher",
        "Pond_PondSpeaker": "PondPondSpeaker",
    },
}


async def engine_formatter(names: list[str], casing: str) -> list[str]:
    return [ENGINE_NAMES[casing][name] for name in names]


@pytest.fixture
def mod() -> Module:
    m = Module("Pond")

    @m.interface
    class HttpFetcher(typing.Protocol):
        @m.function
        def get_url(self, com_url: str) -> Self: ...

        @m.function
        def with_http_client(self, name: str) -> Self: ...

        @m.function
        def fetch(self, from_: str) -> Self: ...

    # Already starts with the module's name: not namespaced again.
    @m.interface
    class PondSpeaker(typing.Protocol):
        @m.function
        def speak(self) -> Self: ...

    @m.object_type
    class Pond:
        fetcher: HttpFetcher = dagger.field()

    return m


def interface(mod: Module, name: str) -> type:
    return mod.get_object(name).cls


def interfaces(mod: Module):
    return [obj for obj in mod._objects.values() if obj.interface]


async def test_resolve_schema_names(mod: Module):
    names = await resolve_schema_names("Pond", interfaces(mod), engine_formatter)

    assert names is not None
    assert names.fields == {
        "com_url": "comURL",
        "fetch": "fetch",
        "from": "from",
        "get_url": "getURL",
        "name": "name",
        "speak": "speak",
        "with_http_client": "withHTTPClient",
    }
    assert names.types == {
        interface(mod, "HttpFetcher"): "PondHTTPFetcher",
        interface(mod, "PondSpeaker"): "PondSpeaker",
    }


async def test_resolve_schema_names_without_interfaces():
    async def fail(*_):
        pytest.fail("unexpected call to the engine")

    assert await resolve_schema_names("Pond", [], fail) is None


async def test_resolve_schema_names_older_engine(
    mod: Module, monkeypatch: pytest.MonkeyPatch
):
    async def unsupported():
        return False

    monkeypatch.setattr(_naming, "supports_identifiers", unsupported)

    assert await resolve_schema_names("Pond", interfaces(mod)) is None


def test_legacy_schema_names():
    # Modules older than v1.0.0 keep the conversion they always had.
    names = SchemaNames()

    class HttpFetcher: ...

    assert names.field_name("get_url") == "getUrl"
    assert names.field_name("com_url") == "comUrl"
    assert names.interface_name("Pond", HttpFetcher) == "PondHttpFetcher"


def selection(obj) -> tuple[str, str, dict]:
    field = obj._ctx.selections[-1]
    return field.type_name, field.name, field.args


@pytest.mark.parametrize(
    ("schema_names", "expected"),
    [
        (
            # older modules
            False,
            ("PondHttpFetcher", "getUrl", {"comUrl": "x"}),
        ),
        (
            # v1.0.0 modules
            True,
            ("PondHTTPFetcher", "getURL", {"comURL": "x"}),
        ),
    ],
)
async def test_interface_call_names(mod: Module, schema_names: bool, expected):
    if schema_names:
        names = await resolve_schema_names("Pond", interfaces(mod), engine_formatter)
        assert names is not None
        mod._schema_names = names
    to_interface_impl.cache_clear()

    impl = to_interface_impl(interface(mod, "HttpFetcher"))
    assert impl.__name__ == expected[0]
    assert impl._graphql_name() == expected[0]

    obj = impl(Context())
    assert selection(obj.get_url(com_url="x")) == expected


async def test_interface_call_renamed_arg(mod: Module):
    # `from_` is registered as `from`: bind by the Python name, send the other.
    to_interface_impl.cache_clear()
    impl = to_interface_impl(interface(mod, "HttpFetcher"))

    obj = impl(Context())
    assert selection(obj.fetch(from_="x")) == (
        "PondHttpFetcher",
        "fetch",
        {"from": "x"},
    )


async def test_load_schema_names(mod: Module, monkeypatch: pytest.MonkeyPatch):
    async def resolve(main_name, ifaces):
        return await resolve_schema_names(main_name, ifaces, engine_formatter)

    monkeypatch.setattr("dagger.mod._module.resolve_schema_names", resolve)
    stale = to_interface_impl(interface(mod, "HttpFetcher"))

    await mod.load_schema_names()

    impl = to_interface_impl(interface(mod, "HttpFetcher"))
    assert impl is not stale
    assert impl.__name__ == "PondHTTPFetcher"


def query_error() -> dagger.QueryError:
    request = types.SimpleNamespace(document=None)
    return dagger.QueryError([QueryErrorValue("can't parse name")], request)


async def test_format_names(monkeypatch: pytest.MonkeyPatch):
    async def format_identifiers(names, casing):
        assert casing is dagger.Casing.CAMEL
        return [ENGINE_NAMES["CAMEL"][n] for n in names]

    monkeypatch.setattr(dag, "format_identifiers", format_identifiers)

    assert await format_names(["get_url", "com_url"], "CAMEL") == [
        "getURL",
        "comURL",
    ]


async def test_format_names_unparseable(monkeypatch: pytest.MonkeyPatch):
    # The engine keeps a name it can't parse as it is; one of those fails a
    # whole batch, so the rest are formatted one by one.
    async def format_identifiers(*_):
        raise query_error()

    class Identifier:
        def __init__(self, name):
            self.name = name

        async def format(self, casing):
            if not self.name.isascii():
                raise query_error()
            return ENGINE_NAMES[casing.value][self.name]

    monkeypatch.setattr(dag, "format_identifiers", format_identifiers)
    monkeypatch.setattr(dag, "identifier", Identifier)

    assert await format_names(["get_url", "café"], "CAMEL") == ["getURL", "café"]
