from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class Workspace:
    """A module for editing code"""

    source: dagger.Directory

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
    def get_source(self) -> dagger.Directory:
        """Get the source code directory from the Workspace"""
        return self.source
