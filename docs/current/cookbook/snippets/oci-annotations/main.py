import sys
import anyio
import datetime
import dagger

async def main():

    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr, workdir=".")) as client:

        # publish app on alpine base
        container = (
          client
          .container()
          .from_("alpine")
          .with_label("org.opencontainers.image.title", "my-alpine")
          .with_label("org.opencontainers.image.version", "1.0")
          .with_label("org.opencontainers.image.created", datetime.datetime.now().strftime("%Y-%m-%dT%H:%M:%S.%f%z"))
          .with_label("org.opencontainers.image.source", "https://github.com/alpinelinux/docker-alpine")
          .with_label("org.opencontainers.image.licenses", "MIT")
        )
        addr = await container.publish("ttl.sh/my-alpine")

    print(addr)

anyio.run(main)
