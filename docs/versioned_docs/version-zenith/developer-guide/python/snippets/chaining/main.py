"""A Dagger module for saying hello world!."""
from dagger import field, function, object_type


@object_type
class MyModule:
    greeting: str = field(default="Hello")
    name: str = field(default="World")

    @function
    def with_greeting(self, greeting: str) -> "MyModule":
        self.greeting = greeting
        return self

    @function
    def with_name(self, name: str) -> "MyModule":
        self.name = name
        return self

    @function
    def message(self) -> str:
        return f"{self.greeting} {self.name}!"
