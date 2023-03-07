import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # get hostname of service container via API
        val = await (
            client.container()
            .from_("alpine")
            .with_exec(["python", "-m", "http.server"])
            .hostname()
        )

    print(val)


anyio.run(main)
