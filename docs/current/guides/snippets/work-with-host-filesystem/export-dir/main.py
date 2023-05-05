import sys
import tempfile

import anyio
import dagger

async def main(hostdir: str):

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

with tempfile.TemporaryDirectory() as hostdir:
    anyio.run(main, hostdir)
