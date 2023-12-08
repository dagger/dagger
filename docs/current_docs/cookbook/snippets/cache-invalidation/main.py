import sys
from datetime import datetime

import anyio

import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # invalidate cache to force execution
        # of second with_exec() operation
        output = await (
            client.pipeline("test")
            .container()
            .from_("alpine")
            .with_exec(["apk", "add", "curl"])
            .with_env_variable("CACHEBUSTER", str(datetime.now()))
            .with_exec(["apk", "add", "zip"])
            .stdout()
        )

    print(output)


anyio.run(main)
