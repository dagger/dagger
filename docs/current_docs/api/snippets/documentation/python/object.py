from typing import Annotated

from dagger import Doc, object_type


@object_type
class MyModule:
    """The object represents a single user of the system."""

    name: Annotated[str, Doc("The name of the user.")]
    age: Annotated[str, Doc("The age of the user.")]
