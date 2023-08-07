import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # get repository at specified branch
        project = (
          client
          .git("https://github.com/dagger/dagger")
          .branch("main")
          .tree()
        )

        # return container with repository
        # at /src path
        # including all files except files beginning with .git
        out = await (
          client
          .container()
          .from_("alpine:latest")
          .with_directory("/src", project, include=["*"], exclude=[".git*"])
          .with_workdir("/src")
          .with_exec(["ls", "-a", "/src"])
          .stdout()
        )

    print(out)

anyio.run(main)
