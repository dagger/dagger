import sys
import anyio
import datetime
import dagger

async def main():
    # define tags
    tags = ["latest", "1.0-alpine", "1.0", "1.0.0"]    

    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr, workdir=".")) as client:
      
        # set secret as string value
        secret = client.set_secret("password", "DOCKER-HUB-PASSWORD")

        # create and publish image with multiple tags
        container = (
          client
          .container()
          .from_("alpine")
        )

        for tag in tags:
            addr = await (
              container
              .with_registry_auth("docker.io", "vikramatdagger", secret)
              .publish(f"vikramatdagger/my-alpine:{tag}")
            )
            print(f"Published at: {addr}")        

anyio.run(main)
