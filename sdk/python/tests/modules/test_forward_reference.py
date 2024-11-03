from dagger.mod import Module

mod = Module()


@mod.object_type
class Foo:
    @mod.function
    def method(self) -> "Foo": ...


def test_method_returns_resolved_forward_reference():
    fn = mod.get_object("Foo").functions["method"]
    assert fn.return_type == Foo
