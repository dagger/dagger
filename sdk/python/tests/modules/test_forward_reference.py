from dagger.mod import Module

mod = Module()


@mod.object_type
class Foo:
    @mod.function
    def method(self) -> "Foo":
        ...


def test_method_returns_resolved_forward_reference():
    resolver = mod.get_resolver(mod.get_resolvers("foo"), "Foo", "method")
    assert resolver.return_type == Foo
