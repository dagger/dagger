from typing_extensions import Self

from dagger.mod import Module

mod = Module()


@mod.object_type
class Foo:
    @mod.function
    def method1(self) -> "Foo": ...

    @mod.function
    def method2(self) -> Self: ...


def test_method_returns_resolved_forward_reference():
    fn = mod.get_object("Foo").functions["method1"]
    assert fn.return_type is Foo


def test_method_returns_resolved_self():
    fn = mod.get_object("Foo").functions["method2"]
    assert fn.return_type is Foo
