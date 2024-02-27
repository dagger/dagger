"""A simple example module."""

from dagger import function, object_type


@object_type
class MyModule:
    """Simple hello world functions."""

    @function
    def hello(self) -> str:
        """Return a hello world message."""
        return "Hello, world"

    @function
    def loud_hello(self) -> str:
        """Return a loud hello world message.

        Loud means all caps.
        """
        return "HELLO, WORLD"
