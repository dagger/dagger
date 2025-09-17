"""A simple example module to say hello.

Further documentation for the module here.
"""

from typing import Annotated

from dagger import Doc, function, object_type


@object_type
class MyModule:
    """Simple hello functions."""

    @function
    def hello(
        self,
        name: Annotated[str, Doc("Who to greet")],
        greeting: Annotated[str, Doc("The greeting to display")],
    ) -> str:
        """Return a greeting."""
        return f"{greeting}, {name}!"

    @function
    def loud_hello(
        self,
        name: Annotated[str, Doc("Who to greet")],
        greeting: Annotated[str, Doc("The greeting to display")],
    ) -> str:
        """Return a loud greeting.

        Loud means all caps.
        """
        return f"{greeting.upper()}, {name.upper()}!"
