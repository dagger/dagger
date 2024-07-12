import os
import time
from typing import Annotated

import anyio

import dagger
from dagger import Doc, dag, field, function, object_type, telemetry
from main.utils import mounted_workdir


@object_type
class Debug:
    """Functions to debug the runtime container."""

    container: Annotated[
        dagger.Container,
        Doc("Base container"),
    ] = field()

    @function
    async def ruff_files(self, src: dagger.Directory) -> str:
        return await self.container.with_(mounted_workdir(src)).with_exec(["ruff", "check", "--show-files"]).stdout()

    @function
    async def workdir(self, src: dagger.Directory) -> dagger.Container:
        with telemetry.get_tracer().start_as_current_span("workdir span (async)"):
            return await self.container.with_(mounted_workdir(src))

    @function
    def source(self) -> dagger.Directory:
        """The directory containing the module's sources."""
        with telemetry.get_tracer().start_as_current_span("source span (lazy)"):
            return dag.current_module().source()

    @function
    def env(self) -> list[str]:
        """Get the environment variables of the runtime container."""
        return sorted(f"{k}={v}" for k, v in os.environ.items())

    @function
    async def cat(self, path: Annotated[str, Doc("The file path in the runtime container")]) -> str:
        """Read the contents of a file."""
        return await anyio.Path(path).read_text()

    @function
    async def mtime(self, path: Annotated[str, Doc("The file path in the runtime container")]) -> str:
        """Get the modification time of a file."""
        stat = await anyio.Path(path).stat()
        return time.ctime(stat.st_mtime)

    @function
    async def tree(
        self,
        path: Annotated[str, Doc("The directory path in the runtime container")] = ".",
        pattern: Annotated[str, Doc("Glob pattern for matching files")] = "**/*",
    ) -> list[str]:
        """List the files in a directory."""
        return [str(p) async for p in anyio.Path(path).glob(pattern)]

    @function
    async def pwd(self) -> str:
        """Get the current working directory's absolute path."""
        return str(await anyio.Path().absolute())
