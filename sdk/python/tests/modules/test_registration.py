from typing import cast

from dagger.mod import Module
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

        def private_method(self) -> str: ...

        @mod.function
        def exposed_method(self) -> str: ...

    def private_function() -> str: ...

    @mod.function
    def exposed_function() -> str: ...

    resolvers = [
        (r.name, r.origin.__name__ if r.origin else None) for r in mod._resolvers
    ]

    assert resolvers == [
        ("exposed_method", "ExposedClass"),
        ("exposed_field", "ExposedClass"),
        ("", "ExposedClass"),
        ("exposed_function", None),
    ]


def test_func_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def fn_with_doc(self):
            """Foo."""

    r = get_resolver(mod, "Foo", "fn_with_doc")

    assert cast(FunctionResolver, r).func_doc == "Foo."
