import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        # use a python:3.11 container
        # mount the source code directory on the host
        # at /src in the container
        # mount the cache volumes to persist dependencies
        source = (
            client.container()
            .from_("python:3.11")
            .with_directory(
                "/src",
                client.host().directory("."),
                exclude=[".venv/", ".cache/", "ci/"],
            )
            .with_mounted_cache(
                "/root/.cache/pip", client.cache_volume("pip-python-311-myapp-myenv")
            )
            .with_mounted_cache(
                "/root/.local/pipx/cache",
                client.cache_volume("pipx-python-311-myapp-myenv"),
            )
            .with_mounted_cache(
                "/root/.cache/hatch",
                client.cache_volume("hatch-python-311-myapp-myenv"),
            )
        )

        # set the working directory in the container
        # install application dependencies
        runner = (
            await source.with_workdir("/src")
            .with_exec(["pip", "install", "-r", "requirements.txt"])
            .sync()
        )

        print(await runner.id())


anyio.run(main)
