from typing import cast

import pytest

from dagger.mod import Module
from dagger.mod._exceptions import NameConflictError, UserError
from dagger.mod._resolver import FunctionResolver


def get_resolver(mod: Module, parent_name: str, resolver_name: str):
    return mod.get_resolver(
        mod.get_resolvers("foo"),
        parent_name,
        resolver_name,
    )


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
        (r.name, r.origin.__name__ if r.origin else None) for r in mod._resolvers
    ]

    assert resolvers == [
        ("exposed_method", "ExposedClass"),
        ("exposed_field", "ExposedClass"),
        ("", "ExposedClass"),
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


def test_func_doc():
    mod = Module()

    @mod.function
    def fn_with_doc():
        """Foo."""

    r = get_resolver(mod, "Foo", "fn_with_doc")

    assert cast(FunctionResolver, r).func_doc == "Foo."
