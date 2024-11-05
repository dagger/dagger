from typing import Annotated

import dagger
from dagger import Ignore, dag, function, object_type


@object_type
class MyModule:
    @function
    async def copy_directory_with_exclusions(
        self,
        source: Annotated[dagger.Directory, Ignore(["*", "!*.md"])],
    ) -> dagger.Container:
        return await (
            dag.container().from_("alpine:latest").with_directory("/src", source).sync()
        )
