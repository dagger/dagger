"""An example for documenting function arguments"""

from typing import Annotated

from dagger import Doc, function, object_type


@object_type
class MyModule:
    @function
    def hello(
        self,
        name: Annotated[str, Doc("Who to greet")],
        greeting: Annotated[str, Doc("The greeting to display")] = "Hello",
    ) -> str:
        return f"{greeting}, {name}!"
