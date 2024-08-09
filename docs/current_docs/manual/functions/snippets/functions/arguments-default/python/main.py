from dagger import function, object_type


@object_type
class MyModule:
    @function
    def hello(self, name: str = "world") -> str:
        return f"Hello, {name}"
