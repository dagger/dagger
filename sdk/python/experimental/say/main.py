from typing import Annotated

import dagger
from dagger.server import command


@command
async def say(
    client: dagger.Client,
    msg: Annotated[str, "What do you want to say?"],
) -> str:
    """Say something with cowsay."""
    return await (
        client.container()
        .from_("python:alpine")
        .with_exec(["pip", "install", "cowsay"])
        .with_entrypoint(["cowsay"])
        .with_exec([msg])
        .stdout()
    )
