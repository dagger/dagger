from typing import Annotated

import dagger
from dagger import  DefaultPath, Ignore, function, object_type, field

@object_type
class Files:
    repo_files: list[str] = field()
    module_files: list[str] = field()
    readme_content: str = field()

@object_type
class MyModule:
    @function
    async def repoFiles(
        self, 
        repo: Annotated[dagger.Directory, DefaultPath("/")],
        moduleFiles: Annotated[dagger.Directory, DefaultPath(".")],
        readmeFile: Annotated[dagger.File, DefaultPath("/README.md")]
    ) -> list[str]:
        return Files(
            await repo.entries(),
            await moduleFiles.entries(),
            await readmeFile.contents()
        )

