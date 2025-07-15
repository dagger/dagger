from dagger import function, object_type


@object_type
class MyModule:
    greeting: str = "Hello"
    name: str = "World"

    @function
    def message(self) -> str:
        return f"{self.greeting}, {self.name}!"
