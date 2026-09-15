from dagger import field, function, object_type

@object_type
class X:
    message: str = field(default="")
    when: str = field(default="", name="Timestamp")
    to: str = field(default="", name="recipient")
    from_: str = field(default="", name="from")

@object_type
class Test:
    @function
    def my_function(self) -> X:
        return X(message="foo", when="now", to="user", from_="admin")
