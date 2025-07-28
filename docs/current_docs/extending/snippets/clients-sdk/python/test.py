"""Run tests for multiple Python versions concurrently."""

import sys

import anyio

import dagger
from dagger import dag


async def test():
    versions = ["3.8", "3.9", "3.10", "3.11"]

    async with dagger.connection(dagger.Config(log_output=sys.stderr)):
        # get reference to the local project
        src = dag.host().directory(".")

        async def test_version(version: str):
            python = (
                dag.container()
                .from_(f"python:{version}-slim-buster")
                # mount cloned repository into image
                .with_directory("/src", src)
                # set current working directory for next commands
                .with_workdir("/src")
                # install test dependencies
                .with_exec(["pip", "install", "-r", "requirements.txt"])
                # run tests
                .with_exec(["pytest", "tests"])
            )

            print(f"Starting tests for Python {version}")

            # execute
            await python.sync()

            print(f"Tests for Python {version} succeeded!")

        # when this block exits, all tasks will be awaited (i.e., executed)
        async with anyio.create_task_group() as tg:
            for version in versions:
                tg.start_soon(test_version, version)

    print("All tasks have finished")


anyio.run(test)
