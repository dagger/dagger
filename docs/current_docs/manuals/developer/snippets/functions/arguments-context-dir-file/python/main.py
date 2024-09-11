from typing import Annotated

import dagger
from dagger import  DefaultPath, function, object_type

@object_type
class MyModule:
    @function
    async def readDir(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> list[str]:
        return await source.entries()

    @function
    async def readFile(
        self,
        source: Annotated[dagger.File, DefaultPath("/README.md")],
    ) -> list[str]:
        return await source.contents()
