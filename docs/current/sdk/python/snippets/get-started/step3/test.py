"""Run tests for a single Python version."""

import sys

import anyio

import dagger


async def test():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # highlight-start
        # get reference to the local project
        src = client.host().directory(".")

        python = (
            client.container().from_("python:3.10-slim-buster")
            # mount cloned repository into image
            .with_mounted_directory("/src", src)
            # set current working directory for next commands
            .with_workdir("/src")
            # install test dependencies
            .with_exec(["pip", "install", "-e", ".[test]"])
            # run tests
            .with_exec(["pytest", "tests"])
        )

        # execute
        await python.exit_code()

    print("Tests succeeded!")
    # highlight-end


if __name__ == "__main__":
    anyio.run(test)
