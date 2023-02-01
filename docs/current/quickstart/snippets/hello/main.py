import sys

import anyio
import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    # initialize Dagger client
    async with dagger.Connection(config) as client:
        # use a node:16-slim container
        # get version
        python = (
            client.container().from_("python:3.11-slim").with_exec(["python", "-V"])
        )

        # execute
        version = await python.stdout()

    # print output
    print(f"Hello from Dagger and {version}")


anyio.run(main)
