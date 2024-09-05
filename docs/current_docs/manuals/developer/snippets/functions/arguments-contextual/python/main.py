from typing import Annotated

import dagger
from dagger import  DefaultPath, Ignore, function, object_type

@object_type
class MyModule:
    @function
    async def repoFiles(
        self, 
        repo: Annotated[dagger.Directory, DefaultPath("/")]
    ) -> list[str]:
        return await repo.entries()

    @function
    async def moduleFiles(
        self, 
        module: Annotated[dagger.Directory, DefaultPath(".")]
    ) -> list[str]:
        return await module.entries()
    
    @function
    async def readme(
        self, 
        readmeFile: Annotated[dagger.File, DefaultPath("/README.md")]
    ) -> list[str]:
        return await readmeFile.contents()
