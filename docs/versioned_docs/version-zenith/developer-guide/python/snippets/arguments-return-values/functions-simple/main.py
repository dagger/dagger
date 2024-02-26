from dagger import function, object_type


@object_type
class MyModule:

    @function
    def hello(self) -> str:
        return "Hello, world"
