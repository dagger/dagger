import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get hostname of service container
        val = await client.container().from_("alpine").with_exec(["hostname"]).stdout()

    print(val)


anyio.run(main)
