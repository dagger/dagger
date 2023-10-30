from dagger.mod import Annotated, Doc, field, function, object_type


@object_type
class Repeater:
    """Repeats a message a given number of times."""

    message: Annotated[
        str,
        Doc("The message to repeat"),
    ] = field(default="Hello")

    times: Annotated[
        int,
        Doc("The number of times to repeat the message"),
    ] = field(default=0)

    @function
    def render(self) -> str:
        """Return the repeated message."""
        return self.message * self.times


@function
def repeater(
    msg: Annotated[str, Doc("The message to repeat")],
    times: Annotated[int, Doc("The number of times to repeat the message")],
) -> Repeater:
    """Return a Repeater instance."""
    return Repeater(message=msg, times=times)
