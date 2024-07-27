from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def simple_directory(self) -> str:
        return await (
            dag.git("https://github.com/dagger/dagger.git")
            .head()
            .tree()
            .terminal()
            .file("README.md")
            .contents()
        )
