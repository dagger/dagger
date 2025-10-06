from dagger import function, object_type


@object_type
class MyModule:
    @function
    def hello(self, names: list[str]) -> str:
        message = "Hello"
        for name in names:
            message += f", {name}"
        return message
