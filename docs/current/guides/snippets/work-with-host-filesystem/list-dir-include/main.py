import sys
import tempfile
import os

import anyio
import dagger

async def main():

    dir = tempfile.gettempdir()
    files = ["foo.txt","bar.txt","baz.rar"]
    count = 1
    for file in files:
        with open(os.path.join(dir, file), "w") as out:
            out.write(str(count))
            count = count+1

    async with dagger.Connection(dagger.Config(log_output=sys.stderr, workdir=dir)) as client:
    
        entries = await client.host().directory(".", None, ["*.rar"]).entries()
        print(entries)

anyio.run(main)
