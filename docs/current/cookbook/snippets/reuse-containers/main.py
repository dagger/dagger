import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stderr)

    async with dagger.Connection(config) as client:
        # build container in one pipeline
        ctr = await (
            client.pipeline("Test")
            .container()
            .from_("alpine")
            .with_exec(["apk", "add", "curl"])
            .sync()
        )

        # get container ID
        cid = await ctr.id()

        # use container in another pipeline via its ID
        await (
            client.container(id=cid)
            .pipeline("Build")
            .with_exec(["curl", "https://dagger.io"])
            .sync()
        )


anyio.run(main)
