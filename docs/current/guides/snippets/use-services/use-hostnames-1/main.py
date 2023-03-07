import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get IP address of service container
        val = await (
            client.container()
            .from_("alpine")
            .with_exec(["sh", "-c", "ip route | grep src"])
            .stdout()
        )

    print(val)


anyio.run(main)
