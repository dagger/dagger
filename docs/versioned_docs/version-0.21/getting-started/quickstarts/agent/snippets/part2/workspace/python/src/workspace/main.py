from typing import Annotated

import dagger
from dagger import Doc, dag, field, function, object_type


@object_type
class Workspace:
    """A module for editing code"""

    source: Annotated[dagger.Directory, Doc("the workspace source code")] = field()

    @function
    async def read_file(
        self,
        path: Annotated[str, Doc("The path to the file in the workspace")],
    ) -> str:
        """Read a file in the Workspace"""
        return await self.source.file(path).contents()

    @function
    def write_file(
        self,
        path: Annotated[str, Doc("The path to the file in the workspace")],
        contents: Annotated[str, Doc("The new contents of the file")],
    ) -> "Workspace":
        """Write a file to the Workspace"""
        self.source = self.source.with_new_file(path, contents)
        return self

    @function
    async def list_files(self) -> str:
        """List all of the files in the Workspace"""
        return await (
            dag.container()
            .from_("alpine:3")
            .with_directory("/src", self.source)
            .with_workdir("/src")
            .with_exec(["tree", "./src"])
            .stdout()
        )

    @function
    async def test(self) -> str:
        """Return the result of running unit tests"""
        node_cache = dag.cache_volume("node")
        return await (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", self.source)
            .with_mounted_cache("/root/.npm", node_cache)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )
