import sys

import anyio

import dagger


async def main():
    for i, file in enumerate(["foo.txt", "bar.txt", "baz.rar"]):
        await (anyio.Path(".") / file).write_text(str(i + 1))

    cfg = dagger.Config(log_output=sys.stderr)

    async with dagger.Connection(cfg) as client:
        entries = (
            await client.host()
            .directory(".", exclude=["*.rar"], include=["*.*"])
            .entries()
        )

    print(entries)


anyio.run(main)
