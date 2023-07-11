import sys

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # setup container with docker socket
        ctr = (
            client.container()
            .from_("docker")
            .with_unix_socket(
                "/var/run/docker.sock",
                client.host().unix_socket("/var/run/docker.sock"),
            )
            .with_exec(["docker", "run", "--rm", "alpine", "uname", "-a"])
        )

        # print docker run
        print(await ctr.stdout())


anyio.run(main)
