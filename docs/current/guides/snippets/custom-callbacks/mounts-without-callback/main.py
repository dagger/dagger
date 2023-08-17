import sys

import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        ctr = client.container().from_("alpine")

        # breaks the chain!
        ctr = add_mounts(ctr, client)

        out = await ctr.with_exec(["ls"]).stdout()

    print(out)


def add_mounts(ctr: dagger.Container, client: dagger.Client):
    return ctr.with_mounted_directory(
        "/foo", client.host().directory("/tmp/foo")
    ).with_mounted_directory("/bar", client.host().directory("/tmp/bar"))


anyio.run(main)
