"""
Run tests for multiple Python versions.
"""

import sys

import anyio

import dagger


async def test():
    # highlight-start
    versions = ["3.7", "3.8", "3.9", "3.10", "3.11"]
    # highlight-end

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get reference to the local project
        src_id = await client.host().directory(".").id()

        # highlight-start
        for version in versions:
            # highlight-end
            python = (
                client.container()
                # highlight-start
                .from_(f"python:{version}-slim-buster")
                # highlight-end
                # mount cloned repository into image
                .with_mounted_directory("/src", src_id)
                # set current working directory for next commands
                .with_workdir("/src")
                # install test dependencies
                .with_exec(["pip", "install", "-e", ".[test]"])
                # run tests
                .with_exec(["pytest", "tests"])
            )

            # highlight-start
            print(f"Starting tests for Python {version}")
            # highlight-end

            # execute
            await python.exit_code()

            # highlight-start
            print(f"Tests for Python {version} succeeded!")

    print("All tasks have finished")
    # highlight-end


if __name__ == "__main__":
    anyio.run(test)
