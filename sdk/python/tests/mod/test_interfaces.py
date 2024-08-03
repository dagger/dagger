import typing

import pytest
from typing_extensions import Self

import dagger
from dagger.client.base import Interface
from dagger.mod import Module
from dagger.mod._utils import (
    is_dagger_interface_type,
    is_dagger_object_type,
)

pytestmark = [
    pytest.mark.anyio,
]


@pytest.fixture
def mod() -> Module:
    m = Module()

    @m.interface
    class Goose(typing.Protocol):
        @m.function
        def speak(self) -> str: ...

    @m.interface
    class Duck(typing.Protocol):
        @m.function
        def quack(self) -> str: ...

        @m.function
        def get_self(self) -> Self: ...

        @m.function
        def get_mob(self) -> list[Self]: ...

        def get_private(self) -> bool: ...

    @m.object_type
    class Pond:
        duck: Duck = dagger.field()

    m.set_module_name("pond")

    return m


@pytest.fixture
async def duck(mod: Module):
    r, t = await mod.get_structured_result(
        "Pond",
        {"duck": "123456"},
        "duck",
        {},
    )
    return r, t


def test_registered_functions(mod: Module):
    assert "quack" in mod.get_object("Duck").functions


async def test_generated_implementation(duck):
    r, t = duck

    assert t.__name__ == "Duck"
    assert is_dagger_interface_type(t)

    assert type(r).__name__ == "PondDuck"
    assert is_dagger_object_type(type(r))

    assert isinstance(r, Interface)
    assert hasattr(r, "quack")
    assert not hasattr(r, "get_private")


async def test_interface_object_chain(duck):
    r, _ = duck
    assert type(r.get_self()) is type(r)
