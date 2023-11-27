import os
import sys

import anyio

import dagger


async def main():
    # define tags
    tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]

    if "DOCKERHUB_USERNAME" not in os.environ:
        msg = "DOCKERHUB_USERNAME environment variable must be set"
        raise OSError(msg)

    if "DOCKERHUB_PASSWORD" not in os.environ:
        msg = "DOCKERHUB_PASSWORD environment variable must be set"
        raise OSError(msg)

    username = os.environ["DOCKERHUB_USERNAME"]
    password = os.environ["DOCKERHUB_PASSWORD"]

    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # set secret as string value
        secret = client.set_secret("password", password)

        # create and publish image with multiple tags
        container = client.container().from_("alpine")

        for tag in tags:
            addr = await container.with_registry_auth(
                "docker.io", username, secret
            ).publish(f"{username}/my-alpine:{tag}")

            print(f"Published at: {addr}")


anyio.run(main)
