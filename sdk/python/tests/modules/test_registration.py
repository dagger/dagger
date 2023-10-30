import pytest

import dagger
from dagger.mod import Annotated, Arg, Module
from dagger.mod._exceptions import NameConflictError, UserError


def test_object_type_resolvers():
    mod = Module()

    @mod.object_type
    class ExposedClass:
        private_field: str
        exposed_field: str = mod.field()

        def private_method(self) -> str:
            ...

        @mod.function
        def exposed_method(self) -> str:
            ...

    def private_function() -> str:
        ...

    @mod.function
    def exposed_function() -> str:
        ...

    resolvers = [
        (r.name, r.origin.__name__ if r.origin else None)
        for r in mod._resolvers  # noqa: SLF001
    ]

    assert resolvers == [
        ("exposed_method", "ExposedClass"),
        ("exposed_field", "ExposedClass"),
        ("exposed_function", None),
    ]


def test_no_main_object():
    mod = Module()

    @mod.object_type
    class Bar:
        @mod.function
        def method(self):
            ...

    with pytest.raises(UserError, match="doesn't define"):
        mod.get_resolvers("foo")


def test_toplevel_and_class_conflict():
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def method(self):
            ...

    @mod.function
    def func():
        ...

    with pytest.raises(NameConflictError, match="not both"):
        mod.get_resolvers("foo")


def test_resolver_name_conflict():
    mod = Module()

    @mod.function
    def foo():
        ...

    @mod.function(name="foo")
    def foo_():
        ...

    with pytest.raises(NameConflictError, match="“Foo.foo” is defined 2 times"):
        mod.get_resolvers("foo")


@pytest.mark.parametrize(
    ("mod_name", "class_name"),
    [
        ("foo", "Foo"),
        ("foo-bar", "FooBar"),
        ("foo_bar", "FooBar"),
        ("fooBar", "FooBar"),
        ("FooBar", "FooBar"),
    ],
)
def test_main_object_name(mod_name, class_name):
    mod = Module()

    @mod.function
    def func():
        ...

    resolvers = mod.get_resolvers(mod_name)
    assert next(iter(resolvers.keys())).name == class_name


@pytest.mark.anyio()
async def test_function_and_arg_name_override():
    mod = Module()

    @mod.function(name="import")
    def import_(from_: Annotated[str, Arg("from")]) -> str:
        return from_

    resolver = mod.get_resolver(mod.get_resolvers("foo"), "Foo", "import")
    result = await mod.get_result(resolver, dagger.JSON("{}"), {"from": "bar"})
    assert result == "bar"


@pytest.mark.anyio()
async def test_field_name_override():
    mod = Module()

    @mod.object_type
    class Foo:
        with_: str = mod.field(default="bar", name="with")

    resolver = mod.get_resolver(mod.get_resolvers("foo"), "Foo", "with")
    result = await mod.get_result(resolver, dagger.JSON("{}"), {})
    assert result == "bar"
