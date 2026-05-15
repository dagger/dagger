from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type

@object_type
class Test:
    @function
    async def too_high_relative_dir_path(
        self,
        backend: Annotated[dagger.Directory, DefaultPath("../../../")],
    ) -> list[str]:
        # The engine should throw an error
        return []

    @function
    async def non_existing_path(
        self,
        dir: Annotated[dagger.Directory, DefaultPath("/invalid")],
    ) -> list[str]:
        # The engine should throw an error
        return []

    @function
    async def too_high_relative_file_path(
        self,
        backend: Annotated[dagger.File, DefaultPath("../../../file.txt")],
    ) -> str:
        # The engine should throw an error
        return ""

    @function
    async def non_existing_file(
        self,
        file: Annotated[dagger.File, DefaultPath("/invalid")],
    ) -> str:
        # The engine should throw an error
        return ""
