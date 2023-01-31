import sys

import anyio
import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        # use a node:16-slim container
        # mount the source code directory on the host
        # at /src in the container
        source = (
            client.container()
            .from_("node:16-slim")
            .with_mounted_directory(
                "/src",
                client.host().directory(".", exclude=["node_modules/", "ci/"]),
            )
        )

        # set the working directory in the container
        # install application dependencies
        runner = source.with_workdir("/src").with_exec(["npm", "install"])

        # run application tests
        out = await runner.with_exec(["npm", "test", "--", "--watchAll=false"]).stderr()

        print(out)

anyio.run(main)
