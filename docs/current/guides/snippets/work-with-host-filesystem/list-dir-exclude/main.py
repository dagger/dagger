import sys
import tempfile

import anyio
import dagger

async def main(workdir: anyio.Path):
    for i, file in enumerate(["foo.txt", "bar.txt", "baz.rar"]):
        await (workdir / file).write_text(str(i + 1))

    cfg = dagger.Config(log_output=sys.stderr, workdir=workdir)

    async with dagger.Connection(cfg) as client:
        entries = await client.host().directory(".", exclude=["*.txt"]).entries()

    print(entries)

with tempfile.TemporaryDirectory() as workdir:
    anyio.run(main, anyio.Path(workdir))
