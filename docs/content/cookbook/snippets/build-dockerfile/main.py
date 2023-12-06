import random
import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        # get build context directory
        context_dir = client.host().directory("/projects/myapp")

        # get Dockerfile in different filesystem location
        dockerfile_path = "/data/myapp/custom.Dockerfile"
        dockerfile = client.host().file(dockerfile_path)

        # add Dockerfile to build context directory
        workspace = context_dir.with_file("custom.Dockerfile", dockerfile)

        # build using Dockerfile
        # publish the resulting container to a registry
        image_ref = await (
            client.container()
            .build(context=workspace, dockerfile="custom.Dockerfile")
            .publish(f"ttl.sh/hello-dagger-{random.SystemRandom().randint(1,10000000)}")
        )

    print(f"Published image to: {image_ref}")


anyio.run(main)
