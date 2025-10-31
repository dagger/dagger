import itertools
from typing import Annotated

import pytest
from typing_extensions import Doc, Self

import dagger
from dagger import dag
from dagger.mod import Module
from dagger.mod._converter import to_typedef
from dagger.mod._exceptions import BadUsageError


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
    def unsupported_top_level() -> str: ...

    fields = list(
        itertools.chain.from_iterable(
            (f.original_name for f in o.fields.values()) for o in mod._objects.values()
        )
    )
    functions = list(
        itertools.chain.from_iterable(
            (f.original_name for f in o.functions.values())
            for o in mod._objects.values()
        )
    )
    assert fields + functions == [
        "exposed_field",
        "exposed_method",
    ]


def test_func_doc():
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def fn_with_doc(self):
            """Foo."""

    assert mod.get_object("Foo").functions["fn_with_doc"].doc == "Foo."


def test_function_deprecated_metadata():
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function(deprecated="Use new method instead")
        def legacy(self):
            """Legacy function."""

    fn = mod.get_object("Foo").functions["legacy"]
    assert fn.deprecated == "Use new method instead"


def test_function_argument_deprecated_metadata():
    mod = Module()

    @mod.object_type
    class Foo:
        @mod.function
        def legacy(
            self,
            value: Annotated[str, dagger.Deprecated("Use new argument instead")],
            empty: Annotated[str, dagger.Deprecated()],
            current: str,
        ) -> str:
            return value

    with pytest.raises(
        BadUsageError,
        match="Can't deprecate required parameter 'value'",
    ):
        _ = mod.get_object("Foo").functions["legacy"].parameters


def test_field_deprecated_metadata():
    mod = Module()

    @mod.object_type
    class Foo:
        legacy: str = mod.field(default="", deprecated="Use new field instead")

    field = mod.get_object("Foo").fields["legacy"]
    assert field.meta.deprecated == "Use new field instead"


def test_object_type_deprecated_metadata():
    mod = Module()

    @mod.object_type(deprecated="Use NewFoo instead")
    class Foo:
        pass

    obj = mod.get_object("Foo")
    assert obj.deprecated == "Use NewFoo instead"


def test_external_constructor_doc():
    mod = Module()

    @mod.object_type
    class External:
        """external docstring"""

        foo: Annotated[str, Doc("a foo walks into a bar")] = "bar"

        @mod.function
        def bar(self) -> str:
            return self.foo

    @mod.object_type
    class Test:
        external = mod.function()(External)
        alternative = mod.function(doc="still external")(External)

    obj = mod.get_object("Test")
    assert obj.functions["external"].doc == "external docstring"
    assert obj.functions["alternative"].doc == "still external"
    # all functions point to the same constructor, with the same arguments
    for fn in obj.functions.values():
        for param in fn.parameters.values():
            assert param.name == "foo"
            assert param.doc == "a foo walks into a bar"
            assert param.default_value == dagger.JSON('"bar"')


def test_external_alt_constructor_doc():
    mod = Module()

    @mod.object_type
    class External:
        """An object"""

        @classmethod
        def create(cls) -> "External":
            """Factory constructor."""
            return cls()

    @mod.object_type
    class Test:
        external = mod.function()(External)

    assert mod.get_object("Test").functions["external"].doc == "Factory constructor."


def test_void_return_type():
    mod = Module()

    @mod.object_type
    class Test:
        @mod.function
        def void(self): ...

    func = mod.get_object("Test").functions["void"]
    assert func.return_type is None
    assert to_typedef(func.return_type) == dag.type_def().with_optional(True).with_kind(
        dagger.TypeDefKind.VOID_KIND
    )


@pytest.mark.anyio
async def test_self_return_type():
    mod = Module()

    @mod.object_type
    class Test:
        @mod.function
        def iden(self) -> Self:
            return self

        @mod.function
        def seq(self) -> list[Self]:
            return [self]

    obj = mod.get_object("Test")
    iden = obj.functions["iden"]
    seq = obj.functions["seq"]
    assert iden.return_type is Test
    assert seq.return_type == list[Test]
    expected = dag.type_def().with_object("Test")
    assert to_typedef(iden.return_type) == expected
    assert to_typedef(seq.return_type) == dag.type_def().with_list_of(expected)
