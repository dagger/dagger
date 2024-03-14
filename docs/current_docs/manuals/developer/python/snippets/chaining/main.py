"""A simple chaining example module."""
from typing import Self

from dagger import function, object_type


@object_type
class MyModule:
    """Functions that chain together."""

    greeting: str = "Hello"
    name: str = "World"

    @function
    def with_greeting(self, greeting: str) -> Self:
        self.greeting = greeting
        return self

    @function
    def with_name(self, name: str) -> Self:
        self.name = name
        return self

    @function
    def message(self) -> str:
        return f"{self.greeting}, {self.name}!"
