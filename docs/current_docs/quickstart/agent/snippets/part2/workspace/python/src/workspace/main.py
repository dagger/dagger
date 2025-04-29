
import dagger
from dagger import dag, function, object_type

@object_type
class Workspace:
    source: dagger.Directory

    @function
    def __init__(self, source: dagger.Directory):
        self.source = source

    @function
    async def read_file(self, path: str) -> str:
        return await self.source.file(path).contents()

    @function
    def write_file(self, path: str, contents: str) -> "Workspace":
        self.source = self.source.with_new_file(path, contents)
        return self

    @function
    async def list_files(self) -> str:
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
        return self.source
