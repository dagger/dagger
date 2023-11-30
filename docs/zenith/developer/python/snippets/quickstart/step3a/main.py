from typing import Annotated

from dagger import Doc, function


@function
def hello_world(
    count: Annotated[int, Doc("The number of potatoes to process")],
    mashed: Annotated[bool, Doc("Whether the potatoes are mashed")] = False,
) -> str:
    """Tell the world how many potatoes you have."""
    if mashed:
        return f"Hello world, I have mashed {count} potatoes"
    return f"Hello world, I have {count} potatoes"
