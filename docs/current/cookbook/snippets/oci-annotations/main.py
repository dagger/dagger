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
        )
        addr = await container.publish("localhost:5000/my-alpine")

    print(addr)        

anyio.run(main)
