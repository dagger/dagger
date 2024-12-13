import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def read_file(self, source: dagger.File) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_file("/tmp/myfile", source)
            .with_exec(["cat", "/tmp/myfile"])
            .stdout()
        )
