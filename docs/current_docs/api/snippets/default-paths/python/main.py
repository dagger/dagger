from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type


@object_type
class MyModule:
    @function
    async def read_dir(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> list[str]:
        return await source.entries()
