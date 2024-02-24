from typing import Annotated

from dagger import Doc, field, function, object_type

@object_type
class PotatoMessage:
    message: str = field()
    from_: str = field(name="from")

@function
def hello_world(
    count: Annotated[int, Doc("The number of potatoes to process")],
    mashed: Annotated[bool, Doc("Whether the potatoes are mashed")] = False,
) -> PotatoMessage:
    """Tell the world how many potatoes you have."""
    if mashed:
        message = f"Hello Daggernauts, I have mashed {count} potatoes"
    else:
        message = f"Hello Daggernauts, I have {count} potatoes"

    return PotatoMessage(
        message=message,
        from_="potato@example.com",
    )
