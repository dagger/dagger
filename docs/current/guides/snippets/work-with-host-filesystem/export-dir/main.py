import sys
import os
import tempfile

import anyio
import dagger

async def main():

    hostdir = tempfile.gettempdir()

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
    
        await (
            client.container()
            .from_("alpine:latest")
            .with_workdir("/tmp")
            .with_exec(["wget", "https://dagger.io"])
            .directory(".")
            .export(hostdir)
        )

    contents = await anyio.Path(hostdir, "index.html").read_text()

    print(contents)

anyio.run(main)
