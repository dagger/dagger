"""A simple hello world example, using a constructor."""
import dataclasses
from typing import Annotated

from dagger import Doc, function, object_type


@object_type
class MyModule:
    """Functions for greeting the world"""

    greeting: str = dataclasses.field(default="Hello", init=False)
    name: Annotated[str, Doc("Who to greet")] = "World"

    @function
    def message(self) -> str:
        """Return the greeting message"""
        return f"{self.greeting}, {self.name}!"
