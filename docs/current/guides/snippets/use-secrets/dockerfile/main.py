import os
import sys

import anyio

import dagger


async def main():
    if "GH_SECRET" not in os.environ:
        msg = "GH_SECRET environment variable must be set"
        raise OSError(msg)

    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # read secret from host variable
        secret = client.set_secret("gh-secret", os.environ["GH_SECRET"])

        # set context directory for Dockerfile build
        context_dir = client.host().directory(".")

        # build using Dockerfile
        # specify secrets for Dockerfile build
        # secrets will be mounted at /run/secrets/[secret-name]
        out = await context_dir.docker_build(
            dockerfile="Dockerfile",
            secrets=[secret],
        ).stdout()

    print(out)


anyio.run(main)
