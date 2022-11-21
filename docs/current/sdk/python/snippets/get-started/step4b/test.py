"""
Run tests for multiple Python versions concurrently.
"""

import sys
import anyio
import dagger

async def test():
    versions = ["3.7", "3.8", "3.9", "3.10", "3.11"]

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # get reference to the local project
        src_id = await client.host().directory(".").id()

        # highlight-start
        # when this block exits, all tasks will be awaited (i.e., executed)
        async with anyio.create_task_group() as tg:
            for version in versions:
                tg.start_soon(test_version, version, src_id, client)
        # highlight-end

        print("All tasks have finished")

async def test_version(version: str, src_id, client: dagger.Client):

    python = (
        client.container()
        .from_(f"python:{version}-slim-buster")

        # mount cloned repository into image
        .with_mounted_directory("/src", src_id)

        # set current working directory for next commands
        .with_workdir("/src")

        # install test dependencies
        .with_exec(["pip", "install", "-e", ".[test]"])

        # run tests
        .with_exec(["pytest", "tests"])
    )

    print(f"Starting tests for Python {version}")

    # execute
    await python.exit_code()

    print(f"Tests for Python {version} succeeded!")

if __name__ == "__main__":
    anyio.run(test)
