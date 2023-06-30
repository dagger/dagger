import sys

import anyio

import dagger

async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        client.container().from_("docker").with_unix_socket("/var/run/docker.sock", client.host().unix_socket("/var/run/docker.sock")).with_exec(["docker", "info"]).sync()              

anyio.run(main)
