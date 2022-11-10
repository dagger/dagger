"""
Execute a command
"""

import sys
import anyio
import dagger

async def test():
    config = dagger.Config(log_output=sys.stderr)

    async with dagger.Connection(config) as client:

        python = (
            client.container()

            # pull container
            .from_("python:3.10-slim-buster")

            # get Python version
            .exec(["python", "-V"])
        )

        # execute
        version = await python.stdout().contents()

        print(f"Hello from Dagger and {version}")

if __name__ == "__main__":
    anyio.run(test)
