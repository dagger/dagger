"""
Run tests for a single Python version.
"""

import sys
import anyio
import dagger

async def test():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # highlight-start
        # get reference to the local project
        src_id = await client.host().directory(".").id()

        python = (
            client.container()
            .from_("python:3.10-slim-buster")

            # mount cloned repository into image
            .with_mounted_directory("/src", src_id)

            # set current working directory for next commands
            .with_workdir("/src")

            # install test dependencies
            .exec(["pip", "install", "-e", ".[test]"])

            # run tests
            .exec(["pytest", "tests"])
        )

        # execute
        await python.exit_code()

        print("Tests succeeded!")
        # highlight-end

if __name__ == "__main__":
    anyio.run(test)
