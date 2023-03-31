import sys

import anyio
import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # set secret as string value
        secret = client.set_secret("password", "DOCKER-HUB-PASSWORD")

        # create container
        ctr = (
            client.container(platform=dagger.Platform("linux/amd64"))
            .from_("nginx:1.23-alpine")
            .with_new_file("/usr/share/nginx/html/index.html", "Hello from Dagger!", 0o400)
        )

        # use secret for registry authentication
        addr = await (
          ctr
          .with_registry_auth("docker.io", "DOCKER-HUB-USERNAME", secret)
          .publish("DOCKER-HUB-USERNAME/my-nginx")
        )

    # print result
    print(f"Published at: {addr}")

anyio.run(main)
