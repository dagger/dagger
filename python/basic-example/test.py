"""
Execute a command
"""

import sys

import anyio

import dagger


async def test():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        python = (
            client.container()
            # pull container
            .from_("python:3.10-slim-buster")
            # get Python version
            .with_exec(["python", "-V"])
        )

        # execute
        version = await python.stdout()

    print(f"Hello from Dagger and {version}")


if __name__ == "__main__":
    anyio.run(test)