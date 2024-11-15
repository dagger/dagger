import anyio

import dagger
from dagger import function, object_type


@object_type
class MyModule:
    @function
    async def copy_file(self, source: dagger.File):
        """Copy a file to the Dagger module runtime container for custom processing"""
        await source.export("foo.txt")
        # your custom logic here
        # for example, read and print the file in the Dagger Engine container
        print(await anyio.Path("foo.txt").read_text())
