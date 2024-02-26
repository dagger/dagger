import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:

    @function
    async def tree(self, src: dagger.Directory, depth: str) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_mounted_directory("/mnt", src)
            .with_workdir("/mnt")
            .with_exec(["apk", "add", "tree"])
            .with_exec(["tree", "-L", depth])
            .stdout()
        )
