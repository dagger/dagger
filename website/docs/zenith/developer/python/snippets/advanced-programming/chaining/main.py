"""A Dagger module for saying hello world!."""
from dagger.mod import field, function, object_type


@object_type
class HelloWorld:
    greeting: str = field(default="Hello")
    name: str = field(default="World")

    @function
    def with_greeting(self, greeting: str) -> "HelloWorld":
        self.greeting = greeting
        return self

    @function
    def with_name(self, name: str) -> "HelloWorld":
        self.name = name
        return self

    @function
    def message(self) -> str:
        return f"{self.greeting} {self.name}!"
