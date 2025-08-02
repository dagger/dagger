import enum
import typing
from dataclasses import InitVar
from typing import Annotated

import pytest
import typing_extensions

import dagger
from dagger import Doc, Name, dag
from dagger.mod import Module
from dagger.mod._exceptions import FatalError

pytestmark = [
    pytest.mark.anyio,
]


@pytest.mark.slow
async def test_unstructure_structure():
    mod = Module()

    @mod.object_type
    class Bar:
        msg: Annotated[str, Doc("Echo message")] = mod.field(default="foobar")
        ctr: Annotated[dagger.Container, Doc("A container")] = mod.field()

        @mod.function
        async def bar(self) -> str:
            return await self.ctr.with_exec(["echo", "-n", self.msg]).stdout()

    @mod.object_type
    class Foo:
        @mod.function
        def foo(self) -> Bar:
            return Bar(ctr=dag.container().from_("alpine"))

    async with dagger.connection():
        parent = await mod.get_result("Foo", {}, "foo", {})
        result = await mod.get_result("Bar", parent, "bar", {})

    assert result == "foobar"


class TestNameOverrides:
    @pytest.fixture(scope="class")
    def mod(self):
        _mod = Module()

        @_mod.object_type
        class Bar:
            with_: str = _mod.field()
            with_x: str = _mod.field(name="withx")

        @_mod.object_type
        class Foo:
            from_: str = _mod.field(default="")

            @_mod.function
            def bar(self) -> Bar:
                return Bar(with_="bar", with_x="bax")

            @_mod.function
            def import_(self, from_: str) -> str:
                return from_

            @_mod.function(name="importx")
            def import_x(self, from_x: Annotated[str, Name("fromx")]) -> str:
                return from_x

        return _mod

    async def test_function_and_arg_name_default(self, mod: Module):
        parent = await mod.get_result("Foo", {}, "", {"from": "foo"})
        assert await mod.get_result("Foo", parent, "from", {}) == "foo"
        assert await mod.get_result("Foo", {}, "import", {"from": "egg"}) == "egg"

    async def test_function_and_arg_name_custom(self, mod: Module):
        assert await mod.get_result("Foo", {}, "importx", {"fromx": "egg"}) == "egg"

    async def test_field_unstructure(self, mod: Module):
        assert await mod.get_result("Foo", {}, "bar", {}) == {
            "with": "bar",
            "withx": "bax",
        }

    async def test_field_structure(self, mod: Module):
        state = {"with": "baz", "withx": "bat"}
        assert await mod.get_result("Bar", state, "with", {}) == "baz"
        assert await mod.get_result("Bar", state, "withx", {}) == "bat"


async def test_method_returns_self():
    mod = Module()

    @mod.object_type
    class Foo:
        message: str = "foo"

        if hasattr(typing, "Self"):

            @mod.function
            def foo(self) -> typing.Self:
                self.message = "foobar"
                return self

        @mod.function
        def bar(self) -> typing_extensions.Self:
            self.message = "barfoo"
            return self

    if hasattr(typing, "Self"):
        assert await mod.get_result("Foo", {}, "foo", {}) == {"message": "foobar"}

    assert await mod.get_result("Foo", {}, "bar", {}) == {"message": "barfoo"}


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
    assert await mod.get_result("Foo", {}, "test", {}) == "foobar"
    assert await mod.get_result("Foo", {}, "", {}) == {
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
    assert await mod.get_result("Foo", {}, "", {}) == {"foo": "barman"}
    assert await mod.get_result("Foo", {}, "", {"bar": "bat"}) == {"foo": "batman"}
    assert await mod.get_result("Foo", {}, "", {"foo": "stool"}) == {"foo": "barstool"}


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
    assert await mod.get_result("Foo", {}, "", {}) == {"foo": "bar"}
    assert await mod.get_result("Foo", {}, "", {"bar": "baz"}) == {"foo": "baz"}


async def test_constructor_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        """Object doc."""

    assert mod.get_object("Foo").get_constructor().doc == "Object doc."


async def test_alt_constructor_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        """Object doc."""

        @classmethod
        def create(cls):
            """Constructor doc."""
            return cls()

    assert mod.get_object("Foo").get_constructor().doc == "Constructor doc."


async def test_alt_async_constructor():
    # The alternative constructor also allows running async code.
    mod = Module()

    async def default_value():
        return "bar"

    @mod.object_type
    class Foo:
        foo: str = mod.field()

        @classmethod
        async def create(cls, bar: str | None = None):
            if bar is None:
                bar = await default_value()
            return cls(foo=bar)

    # Default constructor is still available but argument is mandatory.
    with pytest.raises(TypeError):
        Foo()

    # However, the alternative constructor has a default async value.
    assert (await Foo.create()).foo == "bar"

    # From the API, the alternative constructor should be used.
    assert await mod.get_result("Foo", {}, "", {}) == {"foo": "bar"}
    assert await mod.get_result("Foo", {}, "", {"bar": "baz"}) == {"foo": "baz"}


async def test_no_method_alt_constructor():
    # Should only be decorated as a classmethod.
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="oof")

        def create(self, bar: str):
            return Foo(foo=f"{bar}!")

    assert await mod.get_result("Foo", {}, "", {}) == {"foo": "oof"}
    assert await mod.get_result("Foo", {}, "", {"foo": "foo"}) == {"foo": "foo"}


async def test_no_staticmethod_alt_constructor():
    # Should only be decorated as a classmethod.
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="oof")

        @staticmethod
        def create(bar: str):
            return Foo(foo=f"{bar}!")

    assert await mod.get_result("Foo", {}, "", {}) == {"foo": "oof"}
    assert await mod.get_result("Foo", {}, "", {"foo": "foo"}) == {"foo": "foo"}


async def test_non_constructor_create_function():
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="foo")

        # Non classmethod can use the name `create`.
        @mod.function
        def create(self) -> str:
            return f"{self.foo}bar"

    assert await mod.get_result("Foo", {}, "create", {}) == "foobar"


class TestFunctionFromExternalConstructor:
    @pytest.fixture(scope="class")
    def mod(self):
        _mod = Module()

        @_mod.object_type
        class Bar:
            baz: int = _mod.field(default=144)

        @_mod.object_type
        class Foo:
            egg: str = _mod.field(default="chick")

            # TODO: Written as `function()(Bar)` to avoid Pyright false negative
            # on dataclass field missing an annotation. Fix warning.

            # create a `Test.bar() -> Bar` constructor function
            bas = _mod.function()(Bar)

            # create a `Test.bat() -> Bar` constructor function
            bat = _mod.function(name="bat")(Bar)

        return _mod

    async def test_chain(self, mod: Module):
        # Assert Foo.bar() -> Bar
        chains = [
            ("Foo", "bas", {"baz": 144}),
            ("Bar", "baz", 144),
        ]
        result = {}
        for parent_name, function, expected in chains:
            result = await mod.get_result(parent_name, result, function, {})
            assert result == expected

    async def test_name_overrides_and_inputs(self, mod: Module):
        # Assert Foo.bat(baz=33) -> Bar
        chains = [
            ("Foo", "bat", {"baz": 33}),
            ("Bar", "baz", {}),
        ]
        result = {}
        for parent_name, function, inputs in chains:
            result = await mod.get_result(parent_name, result, function, inputs)
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

        assert await mod.get_result(origin, {}, "test", {}) == {"egg": "white"}


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

    assert await mod.get_result("Foo", {}, "bar", {"bat": "bat"}) == {"bar": "bat"}


async def test_constructor_with_init_var():
    mod = Module()

    @mod.object_type
    class Foo:
        foo: str = mod.field(default="foo")
        bar: InitVar[str] = mod.field(default="bar")

        def __post_init__(self, bar: str):
            self.foo = self.foo + bar

    assert await mod.get_result("Foo", {}, "foo", {}) == "foobar"
    assert await mod.get_result("Foo", {}, "", {"foo": "rab", "bar": "oof"}) == {
        "foo": "raboof",
    }
    with pytest.raises(FatalError):
        await mod.get_result("Foo", {}, "bar", {})


def test_exposed_field_not_in_constructor():
    mod = Module()

    @mod.object_type
    class Foo:
        bat: str
        bar: str = mod.field(default="man", init=False)

    with pytest.raises(TypeError, match="unexpected keyword argument 'bar'"):
        Foo(bat="man", bar="stool")


async def test_enum_conversion():
    mod = Module()

    @mod.enum_type
    class Custom(enum.Enum):
        ONE = "1"
        TWO = "2"

    @mod.enum_type
    class Primitive(str, enum.Enum):
        THREE = "3"
        FOUR = "4"

    @mod.enum_type
    class Compat(dagger.Enum):
        FIVE = "5"
        SIX = "6"

    @mod.object_type
    class Test:
        custom: Custom = mod.field(default=Custom.ONE)

        @mod.function
        def unstruct_custom(self) -> Custom:
            return Custom.ONE

        @mod.function
        def struct_custom(self, val: Custom) -> str:
            return str(val)

        @mod.function
        def unstruct_primitive(self) -> Primitive:
            return Primitive.THREE

        @mod.function
        def struct_primitive(self, val: Primitive) -> str:
            return str(val)

        @mod.function
        def unstruct_compat(self) -> Compat:
            return Compat.FIVE

        @mod.function
        def struct_compat(self, val: Compat) -> str:
            return repr(val)

    obj, _ = await mod.get_structured_result("Test", {}, "", {})
    assert obj.custom == Custom.ONE

    obj, _ = await mod.get_structured_result("Test", {}, "", {"custom": "TWO"})
    assert obj.custom == Custom.TWO

    assert await mod.get_result("Test", {}, "unstruct_custom", {}) == "ONE"
    assert (
        await mod.get_result("Test", {}, "struct_custom", {"val": "TWO"})
        == "Custom.TWO"
    )

    assert await mod.get_result("Test", {}, "unstruct_primitive", {}) == "THREE"
    assert (
        await mod.get_result("Test", {}, "struct_primitive", {"val": "FOUR"})
        == "Primitive.FOUR"
    )

    assert await mod.get_result("Test", {}, "unstruct_compat", {}) == "FIVE"
    assert await mod.get_result("Test", {}, "struct_compat", {"val": "SIX"}) == repr(
        Compat.SIX
    )
