import sys

import anyio
import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr, workdir="..")) as client:

        print("foo")

anyio.run(main)
