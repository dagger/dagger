import sys

import anyio
import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr, workdir="..")) as client:
    
        entries = await client.host().directory(".").entries()
        print(entries)

anyio.run(main)
