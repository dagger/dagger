from dagger import field, function, object_type

@object_type
class Bar:
    msg: str = field()

@object_type
class Foo:
    msg_container: Bar = field()

@object_type
class Test:
    @function
    def my_function(self) -> Foo:
        return Foo(msg_container=Bar(msg="hello world"))
