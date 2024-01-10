import os
import sys

import anyio

import dagger


async def main():
    # check for required variables in host environment
    for var in ["REGISTRY_ADDRESS", "REGISTRY_USERNAME", "REGISTRY_PASSWORD"]:
        if var not in os.environ:
            msg = f'"{var}" environment variable must be set'
            raise OSError(msg)

    # initialize Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # set registry password as Dagger secret
        secret = client.set_secret("password", os.environ["REGISTRY_PASSWORD"])

        # get reference to the project directory
        source = client.host().directory(".", exclude=["node_modules", "ci"])

        # use a node:18-slim container
        node = client.container(platform=dagger.Platform("linux/amd64")).from_(
            "node:18-slim"
        )

        # mount the project directory
        # at /src in the container
        # set the working directory in the container
        # install application dependencies
        # build application
        # set default arguments
        app = (
            node.with_directory("/src", source)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
            .with_exec(["npm", "run", "build"])
            .with_default_args(["npm", "start"])
        )

        # publish image to registry
        # at registry path [registry-username]/myapp
        # print image address
        username = os.environ["REGISTRY_USERNAME"]
        address = await app.with_registry_auth(
            os.environ["REGISTRY_ADDRESS"], username, secret
        ).publish(f"{username}/myapp")

    print(f"Published image to: {address}")


anyio.run(main)
