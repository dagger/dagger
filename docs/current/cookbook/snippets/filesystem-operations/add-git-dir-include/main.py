import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get repository at specified branch
        project = client.git("https://github.com/dagger/dagger").branch("main").tree()

        # return container with repository
        # at /src path
        # including only *.md files
        out = await (
            client.container()
            .from_("alpine:latest")
            .with_directory("/src", project, include=["*.md"])
            .with_workdir("/src")
            .with_exec(["ls", "/src"])
            .stdout()
        )

    print(out)


anyio.run(main)
