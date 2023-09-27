import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stderr)

    async with dagger.Connection(config) as client:
        # use a python:3.11 container
        # mount the source code directory on the host
        # at /src in the container
        # mount the cache volumes to persist dependencies
        source = (
            client.container()
            .from_("python:3.11")
            .with_directory("/src", client.host().directory("."))
            .with_workdir("/src")
            .with_mounted_cache("/root/.cache/pip", client.cache_volume("python-311"))
        )

        # set the working directory in the container
        # install application dependencies
        await source.with_exec(["pip", "install", "-r", "requirements.txt"]).sync()


anyio.run(main)
