import os
import sys
import tempfile
from pathlib import Path

import anyio

import dagger


async def main(workdir: anyio.Path):
    folder = Path(workdir)
    for subdir in ["foo", "bar", "baz"]:
        folder.joinpath(Path(subdir)).mkdir()
        for file in [".txt", ".out", ".rar"]:
            folder.joinpath(Path(subdir), str(subdir + file)).write_text(str(subdir))
        folder = folder / subdir

    cfg = dagger.Config(log_output=sys.stderr, workdir=workdir)

    async with dagger.Connection(cfg) as client:
        daggerdirectory = await client.host().directory(
            ".", include=["**/*.rar", "**/*.txt"], exclude=["**.out"]
        )

        folder = "." + os.sep
        for _, subdir in enumerate(["foo", "bar", "baz"]):
            folder = folder = Path(folder) / subdir
            entries = await daggerdirectory.entries(path=str(folder))
            print("In", subdir, ":", entries)


with tempfile.TemporaryDirectory() as workdir:
    anyio.run(main, anyio.Path(workdir))
