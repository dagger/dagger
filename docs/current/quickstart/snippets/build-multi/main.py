import random
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
        # first stage
        # build application
        build_dir = test.with_exec(["npm", "run", "build"]).directory("./build")

        # second stage
        # use an nginx:alpine container
        # copy the build/ directory from the first stage
        # publish the resulting container to a registry
        image_ref = await (
            client.container()
            .from_("nginx:alpine")
            .with_directory("/usr/share/nginx/html", build_dir)
            .publish(f"ttl.sh/hello-dagger-{random.randint(0, 10000000)}")
        )

    print(f"Published image to: {image_ref}")
    # highlight-end


anyio.run(main)
