import sys

import anyio

import dagger

from .alpine import version


# initialize Dagger client
# pass client to method imported from another module
async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        print(await version(client))


if __name__ == "__main__":
    anyio.run(main)
