from dagger import field, function, object_type

@object_type
class Bar:
    msg: str = field()

@object_type
class Foo:
    msg_container: Bar = field()

@object_type
class OptionalFoo:
    msg_container: Bar | None = field(default=None)

    @function
    def with_message(self, message: str) -> "OptionalFoo":
        self.msg_container = Bar(msg=message)
        return self

    @function
    def message(self) -> str:
        return self.msg_container.msg if self.msg_container else "empty"


@object_type
class Test:
    @function
    def my_function(self) -> Foo:
        return Foo(msg_container=Bar(msg="hello world"))

    @function
    def optional_foo(self, populated: bool = True) -> OptionalFoo:
        return OptionalFoo(msg_container=Bar(msg="hello world") if populated else None)
