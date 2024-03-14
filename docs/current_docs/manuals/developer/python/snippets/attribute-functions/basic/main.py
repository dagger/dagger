"""An example exposing Dagger Functions for object attributes."""
from typing import Annotated

from dagger import Doc, field, function, object_type


@object_type
class MyModule:
    """Functions for greeting the world"""

    greeting: Annotated[str, Doc("The greeting to use")] = field(default="Hello")
    name: Annotated[str, Doc("Who to greet")] = "World"

    @function
    def message(self) -> str:
        """Return the greeting message"""
        return f"{self.greeting}, {self.name}!"
