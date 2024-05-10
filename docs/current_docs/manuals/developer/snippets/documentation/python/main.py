"""A simple example module to say hello."""
from typing import Annotated

from dagger import Doc, function, object_type


@object_type
class MyModule:
    """Simple hello functions."""

    """A function to say hello."""

    @function
    def hello(
        self,
        name: Annotated[str, Doc("Who to greet")],
        greeting: Annotated[str, Doc("The greeting to display")] = "Hello",
    ) -> str:
        """Return a greeting."""
        return f"{greeting}, {name}!"

    """A function to say a loud hello."""

    @function
    def loud_hello(
        self,
        name: Annotated[str, Doc("Who to greet")],
        greeting: Annotated[str, Doc("The greeting to display")] = "Hello",
    ) -> str:
        """Return a loud greeting.

        Loud means all caps.
        """
        return f"{greeting.upper()}, {name.upper()}!"
