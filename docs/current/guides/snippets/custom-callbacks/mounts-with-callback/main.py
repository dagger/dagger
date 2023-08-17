import sys
import anyio

import dagger


async def main():
    config = dagger.Config(log_output=sys.stdout)

    async with dagger.Connection(config) as client:
        out = await (
            client.container()
            .from_("alpine")
            .with_(mounts(client))
            .with_exec(["ls"])
            .stdout()
        )

    print(out)


def mounts(client: dagger.Client):
    def _mounts(ctr: dagger.Container):
        return ctr.with_mounted_directory(
            "/foo", client.host().directory("/tmp/foo")
        ).with_mounted_directory("/bar", client.host().directory("/tmp/bar"))

    return _mounts


anyio.run(main)
