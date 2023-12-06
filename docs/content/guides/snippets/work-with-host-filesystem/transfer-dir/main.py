import sys

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        out = await (
            client.container()
            .from_("alpine:latest")
            .with_directory("/host", client.host().directory("."))
            .with_exec(["ls", "/host"])
            .stdout()
        )

    print(out)


anyio.run(main)
