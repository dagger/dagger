from dagger import function, object_type


@object_type
class MyModule:

    @function
    def hello(self, name: str | None) -> str:
        if name is None:
            name = "world"
        return f"Hello, {name}"
