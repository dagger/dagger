from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def copy_file(
        self,
        f: Annotated[dagger.File, Doc("Source file")],
    ) -> dagger.Container:
        """Return a container with a specified file"""
        name = await f.name()
        return dag.container().from_("alpine:latest").with_file(f"/src/{name}", f)
