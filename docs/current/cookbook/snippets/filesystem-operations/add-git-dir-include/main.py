import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
<<<<<<< HEAD
        # get repository at specified branch
        project = client.git("https://github.com/dagger/dagger").branch("main").tree()
=======

        # get repository at specified branch
        project = (
          client
          .git("https://github.com/dagger/dagger")
          .branch("main")
          .tree()
        )
>>>>>>> 9cafd301 (Added recipes)

        # return container with repository
        # at /src path
        # including only *.md files
        out = await (
<<<<<<< HEAD
            client.container()
            .from_("alpine:latest")
            .with_directory("/src", project, include=["*.md"])
            .with_workdir("/src")
            .with_exec(["ls", "/src"])
            .stdout()
=======
          client
          .container()
          .from_("alpine:latest")
          .with_directory("/src", project, include=["*.md"])
          .with_workdir("/src")
          .with_exec(["ls", "/src"])
          .stdout()
>>>>>>> 9cafd301 (Added recipes)
        )

    print(out)

<<<<<<< HEAD

=======
>>>>>>> 9cafd301 (Added recipes)
anyio.run(main)
