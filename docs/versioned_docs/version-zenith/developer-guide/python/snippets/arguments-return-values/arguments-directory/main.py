import dagger
from dagger import dag, object_type, function

@object_type
class MyModule:

    @function
    async def tree(dir: dagger.Directory) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_mounted_directory("/mnt", dir)
            .with_workdir("/mnt")
            .with_exec(["tree"])
            .stdout()
        )
