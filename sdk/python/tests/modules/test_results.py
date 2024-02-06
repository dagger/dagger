import json
from dataclasses import InitVar
from typing import Annotated, cast

import pytest
from typing_extensions import Self

import dagger
from dagger import Arg, Doc, dag
from dagger.mod import Module
from dagger.mod._exceptions import FatalError
from dagger.mod._resolver import FunctionResolver

pytestmark = [
    pytest.mark.anyio,
]


def get_resolver(mod: Module, parent_name: str, resolver_name: str):
    return mod.get_resolver(
        mod.get_resolvers("foo"),
        parent_name,
        resolver_name,
    )


async def get_result(
    mod: Module,
    parent_name: str,
    parent: dict,
    resolver_name: str,
    inputs: dict,
):
    return await mod.get_result(
        get_resolver(mod, parent_name, resolver_name),
        dagger.JSON(json.dumps(parent)),
        inputs,
    )


@pytest.mark.slow()
async def test_unstructure_structure():
    mod = Module()

    @mod.object_type
    class Bar:
        msg: Annotated[str, Doc("Echo message")] = mod.field(default="foobar")
        ctr: Annotated[dagger.Container, Doc("A container")] = mod.field()

        @mod.function
        async def bar(self) -> str:
            return await self.ctr.with_exec(["echo", "-n", self.msg]).stdout()

    @mod.function
    def foo() -> Bar:
        return Bar(ctr=dag.container().from_("alpine"))

    async with dagger.connection():
        resolver = mod.get_resolver(mod.get_resolvers("foo"), "Foo", "foo")
        result = await mod.get_result(resolver, dagger.JSON({}), {})

        parent = dagger.JSON(json.dumps(result))

        resolver = mod.get_resolver(mod.get_resolvers("foo"), "Bar", "bar")
        result = await mod.get_result(resolver, parent, {})

        assert result == "foobar"


class TestNameOverrides:
    @pytest.fixture(scope="class")
    def mod(self):
        _mod = Module()

        @_mod.object_type
        class Bar:
            with_: str = _mod.field(name="with")

        @_mod.function
        def bar() -> Bar:
            return Bar(with_="bar")

        @_mod.function(name="import")
        def import_(from_: Annotated[str, Arg("from")]) -> str:
            return from_

        return _mod

    async def test_function_and_arg_name(self, mod: Module):
        assert await get_result(mod, "Foo", {}, "import", {"from": "egg"}) == "egg"

    async def test_field_unstructure(self, mod: Module):
        assert await get_result(mod, "Foo", {}, "bar", {}) == {"with": "bar"}

    async def test_field_structure(self, mod: Module):
        assert await get_result(mod, "Bar", {"with": "baz"}, "with", {}) == "baz"


async def test_method_returns_self():
    mod = Module()

    @mod.object_type
    class Foo:
        message: str = "foo"

        @mod.function
        def bar(self) -> Self:
            self.message = "foobar"
            return self

    assert await get_result(mod, "Foo", {}, "bar", {}) == {"message": "foobar"}


async def test_constructor_post_init():
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="foo")
        bar: str = mod.field(default="bar")
        test: str = mod.field(init=False)

        def __post_init__(self):
            self.test = self.foo + self.bar

    assert Foo().test == "foobar"
    assert Foo(foo="oof", bar="rab").test == "oofrab"
    assert await get_result(mod, "Foo", {}, "test", {}) == "foobar"
    assert await get_result(mod, "Foo", {}, "", {}) == {
        "foo": "foo",
        "bar": "bar",
        "test": "foobar",
    }


async def test_overridden_init_constructor():
    mod = Module()

    @mod.object_type
    class Foo:
        # Default should be ignored due to __init__ override
        # but still exposed as a field due to mod.field().
        foo: str = mod.field(default="foo")

        def __init__(self, bar: str = "bar", foo: str = "man"):
            self.foo = bar + foo

    assert Foo().foo == "barman"
    assert Foo(bar="bat").foo == "batman"
    assert await get_result(mod, "Foo", {}, "", {}) == {"foo": "barman"}
    assert await get_result(mod, "Foo", {}, "", {"bar": "bat"}) == {"foo": "batman"}
    assert await get_result(mod, "Foo", {}, "", {"foo": "stool"}) == {"foo": "barstool"}


async def test_alt_constructor():
    # __init__ is not really a constructor, it's a part of one.
    # First, an instance is created via __new__ and then __init__ is called
    # to initialize. This means that if a complex field is required, it needs
    # to allow and default to a sentinel value (e.g., `str | None = None`)
    # only to change it later. Depending on the use case, it might be better
    # to use a factory method instead. Thus the alternative constructor,
    # conventionally a classmethod named `create`.
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="oof")

        @classmethod
        def create(cls, bar: str = "bar"):
            return cls(foo=bar)

    assert Foo().foo == "oof"
    assert Foo.create().foo == "bar"
    assert await get_result(mod, "Foo", {}, "", {}) == {"foo": "bar"}
    assert await get_result(mod, "Foo", {}, "", {"bar": "baz"}) == {"foo": "baz"}


async def test_constructor_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        """Object doc."""

    constructor = cast(FunctionResolver, get_resolver(mod, "Foo", ""))
    assert constructor.func_doc == "Object doc."


async def test_alt_constructor_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        """Object doc."""

        @classmethod
        def create(cls):
            """Constructor doc."""
            return cls()

    constructor = cast(FunctionResolver, get_resolver(mod, "Foo", ""))
    assert constructor.func_doc == "Constructor doc."


async def test_alt_async_constructor():
    # The alternative constructor also allows running async code.
    mod = Module()

    async def default_value():
        return "bar"

    @mod.object_type
    class Foo:
        foo: str = mod.field()

        @classmethod
        async def create(cls, foo: str | None = None):
            if foo is None:
                foo = await default_value()
            return cls(foo=foo)

    # Default constructor is still available but argument is mandatory.
    with pytest.raises(TypeError):
        Foo()

    # However, the alternative constructor has a default async value.
    assert (await Foo.create()).foo == "bar"

    # From the API, the alternative constructor should be used.
    assert await get_result(mod, "Foo", {}, "", {}) == {"foo": "bar"}
    assert await get_result(mod, "Foo", {}, "", {"foo": "baz"}) == {"foo": "baz"}


async def test_no_method_alt_constructor():
    # Should only be decorated as a classmethod.
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="oof")

        def create(self, bar: str):
            return Foo(foo=f"{bar}!")

    assert await get_result(mod, "Foo", {}, "", {}) == {"foo": "oof"}
    assert await get_result(mod, "Foo", {}, "", {"foo": "foo"}) == {"foo": "foo"}


async def test_no_staticmethod_alt_constructor():
    # Should only be decorated as a classmethod.
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="oof")

        @staticmethod
        def create(bar: str):
            return Foo(foo=f"{bar}!")

    assert await get_result(mod, "Foo", {}, "", {}) == {"foo": "oof"}
    assert await get_result(mod, "Foo", {}, "", {"foo": "foo"}) == {"foo": "foo"}


async def test_non_constructor_create_function():
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="foo")

        # Non classmethod can use the name `create`.
        @mod.function
        def create(self) -> str:
            return f"{self.foo}bar"

    assert await get_result(mod, "Foo", {}, "create", {}) == "foobar"


class TestFunctionFromExternalConstructor:
    @pytest.fixture(scope="class")
    def mod(self):
        _mod = Module()

        @_mod.object_type
        class Bar:
            baz: int = _mod.field(default=144)

        @_mod.object_type
        class Test:
            egg: str = _mod.field(default="chick")

            # TODO: Written as `function()(Bar)` to avoid Pyright false negative
            # on dataclass field missing an annotation. Fix warning.

            # create a `Test.bar() -> Bar` constructor function
            bas = _mod.function()(Bar)

            # create a `Test.bat() -> Bar` constructor function
            bat = _mod.function(name="bat")(Bar)

        # create a `Foo.test() -> Test` constructor function
        _mod.function(Test)

        # creates a `Foo.tset() -> Test` constructor function
        _mod.function(Test, name="tset")

        return _mod

    async def test_chain(self, mod: Module):
        # Assert Foo.test().bar() -> Bar
        chains = [
            ("Foo", "test", {"egg": "chick"}),
            ("Test", "bas", {"baz": 144}),
            ("Bar", "baz", 144),
        ]
        result = {}
        for parent_name, resolver, expected in chains:
            result = await get_result(mod, parent_name, result, resolver, {})
            assert result == expected

    async def test_name_overrides_and_inputs(self, mod: Module):
        # Assert Foo.tset(egg="hen").bat(baz=33) -> Bar
        chains = [
            ("Foo", "tset", {"egg": "hen"}),
            ("Test", "bat", {"baz": 33}),
            ("Bar", "baz", {}),
        ]
        result = {}
        for parent_name, resolver, inputs in chains:
            result = await get_result(mod, parent_name, result, resolver, inputs)
            if inputs:
                assert result == inputs
        assert result == 33

    @pytest.mark.parametrize("origin", ["Foo", "Bar"])
    async def test_resolver_with_multiple_origins(self, origin):
        mod = Module()

        @mod.object_type
        class Test:
            egg: str = mod.field(default="white")

        @mod.object_type
        class Foo:
            foo: str = mod.field(default="foo")

            test = mod.function()(Test)

        @mod.object_type
        class Bar:
            bar: str = mod.field(default="bar")

            test = mod.function()(Test)

        assert get_resolver(mod, "Test", "").origin is Test
        assert get_resolver(mod, origin, "").origin is locals()[origin]
        assert get_resolver(mod, origin, "test").origin is locals()[origin]
        assert await get_result(mod, origin, {}, "test", {}) == {"egg": "white"}


async def test_external_alt_constructor():
    mod = Module()

    @mod.object_type
    class Bar:
        bar: str = mod.field(default="bar")

        @classmethod
        def create(cls, bat: str):
            return cls(bar=bat)

    @mod.object_type
    class Foo:
        bar = mod.function()(Bar)

    assert await get_result(mod, "Foo", {}, "bar", {"bat": "bat"}) == {"bar": "bat"}


async def test_constructor_with_init_var():
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="foo")
        bar: InitVar[str] = mod.field(default="bar")

        def __post_init__(self, bar: str):
            self.foo = self.foo + bar

    assert await get_result(mod, "Foo", {}, "foo", {}) == "foobar"
    assert await get_result(mod, "Foo", {}, "", {"foo": "rab", "bar": "oof"}) == {
        "foo": "raboof",
    }
    with pytest.raises(FatalError):
        await get_result(mod, "Foo", {}, "bar", {})


async def test_can_call_top_level_function():
    mod = Module()

    @mod.function
    def foo(msg: str) -> str:
        return bar(msg)

    @mod.function
    def bar(msg: str) -> str:
        return msg

    assert await get_result(mod, "Foo", {}, "foo", {"msg": "foobar"}) == "foobar"


async def test_can_call_top_level_async_function():
    mod = Module()

    @mod.function
    async def foo(msg: str) -> str:
        return await bar(msg)

    @mod.function
    async def bar(msg: str) -> str:
        return msg

    assert await bar("rab") == "rab"
    assert await get_result(mod, "Foo", {}, "foo", {"msg": "foobar"}) == "foobar"


def test_exposed_field_not_in_constructor():
    mod = Module()

    @mod.object_type
    class Foo:
        bat: str
        bar: str = mod.field(default="man", init=False)

    with pytest.raises(TypeError, match="unexpected keyword argument 'bar'"):
        Foo(bat="man", bar="stool")
