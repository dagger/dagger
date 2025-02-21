from typing import Annotated

import dagger
from dagger import DefaultPath, dag, function, object_type


@object_type
class MyModule:
    source: Annotated[dagger.Directory, DefaultPath(".")]

    @function
    async def foo(self) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_mounted_directory("/app", self.source)
            .directory("/app")
            .entries()
        )
