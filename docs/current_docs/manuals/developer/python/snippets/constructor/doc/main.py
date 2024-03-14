"""A hello world example, using a constructor."""
from typing import Annotated

from dagger import Doc, function, object_type


@object_type
class MyModule:
    """Functions for greeting the world"""

    greeting: Annotated[str, Doc("The greeting to use")] = "Hello"
    name: Annotated[str, Doc("Who to greet")] = "World"

    @function
    def message(self) -> str:
        """Return the greeting message"""
        return f"{self.greeting}, {self.name}!"
