from typing import Annotated

import dagger
from dagger import Ignore, dag, function, object_type


@object_type
class MyModule:
    @function
    async def foo(
        self,
        source: Annotated[dagger.Directory, Ignore(["*", "!**/*.py"])],
    ) -> dagger.Container:
        return await (
            dag.container().from_("alpine:latest").with_directory("/src", source).sync()
        )
