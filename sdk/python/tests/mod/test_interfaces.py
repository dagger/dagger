import typing

import pytest

import dagger
from dagger.client.base import Interface
from dagger.mod import Module

pytestmark = [
    pytest.mark.anyio,
]


@pytest.fixture
def mod() -> Module:
    m = Module()

    @m.interface
    class Duck(typing.Protocol):
        @m.function
        def quack(self) -> str: ...

        @m.function
        def get_fancy(self) -> str: ...

        def get_private(self) -> bool: ...

    @m.object_type
    class Pond:
        duck: Duck = dagger.field()
        mallard: Duck = dagger.field()

        @m.function
        def message(self) -> str:
            return "foo"

        @m.function
        def get_duck(self) -> Duck:
            return self.duck

    return m


def duck() -> type:
    m = Module()

    @m.interface
    class Duck(typing.Protocol):
        @m.function
        def quack(self) -> str: ...

        @m.function
        def get_fancy(self) -> str: ...

    return Duck


@pytest.fixture
def quack(mod: Module):
    return mod.get_object("Duck").functions["quack"]


def test_registered_functions(mod: Module):
    assert "quack" in mod.get_object("Duck").functions


async def test_generated_implementation(mod: Module):
    r, t = await mod.get_structured_result(
        "Pond",
        {"duck": "123456", "mallard": "654321"},
        "duck",
        {},
    )
    assert t.__name__ == "Duck"
    assert type(r).__name__ == "DuckImpl"
    assert r._graphql_name() == "Duck"
    assert isinstance(r, Interface)
