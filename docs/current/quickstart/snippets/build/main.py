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
        test = runner.with_exec(["npm", "test", "--", "--watchAll=false"])

        # highlight-start
        # build application
        # writhe the build output to the host
        build_dir = (
            test.with_exec(["npm", "run", "build"])
            .directory("./build")
        )

        await build_dir.export("./build")

        e = await build_dir.entries()

        print(f"build dir contents:\n{e}")
        # highlight-end


anyio.run(main)
