import random
import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        # set build context
        context_dir = client.host().directory(".")

        # build using Dockerfile
        # publish the resulting container to a registry
        image_ref = await context_dir.docker_build().publish(
            f"ttl.sh/hello-dagger-{random.randrange(10 ** 8)}"
        )

    print(f"Published image to: {image_ref}")


anyio.run(main)
