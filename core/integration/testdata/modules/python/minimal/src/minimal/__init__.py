from dagger import function, object_type


@object_type
class Minimal:
    @function
    def hello(self) -> str:
        return "hello"
