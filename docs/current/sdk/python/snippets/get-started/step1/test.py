"""
Execute a command
"""

import anyio
import dagger

async def test():
    async with dagger.Connection() as client:

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
