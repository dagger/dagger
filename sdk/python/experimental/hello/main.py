from typing import Annotated

import dagger
from dagger.server import argument, command, commands


@commands
class Hello:
    greeting: str = "Hello"

    @command
    def say(self, msg: Annotated[str, "The message to greet"]) -> str:
        """Say hello."""
        return f"{self.greeting} {msg}!"

    @command
    async def html(
        self,
        client: dagger.Client,
        from_: Annotated[str, argument(description="The image source", name="from")],
    ) -> str:
        """Get the HTML of the dagger.io website."""
        return await (
            client.container()
            .from_(from_)
            .with_exec(["apk", "add", "curl"])
            .with_exec(["curl", "https://dagger.io/"])
            .stdout()
        )


@command
def hello() -> Hello:
    """Some example 'hello, world'-type commands."""
    return Hello()
