import sys

import anyio

import dagger


async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        out = await (
            client.container()
            .from_("alpine:latest")
            .with_directory("/host", client.host().directory("/tmp/sandbox"))
            .with_exec(["/bin/sh", "-c", "`echo foo > /host/bar`"])
            .directory("/host")
            .export("/tmp/sandbox")
        )

    print(out)


anyio.run(main)
