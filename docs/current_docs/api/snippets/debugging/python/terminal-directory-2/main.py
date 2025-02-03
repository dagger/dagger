from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def advanced_directory(self) -> str:
        return await (
            dag.git("https://github.com/dagger/dagger.git")
            .head()
            .tree()
            .terminal(
                cmd=["/bin/bash"],
                experimentalprivilegednesting=false,
                insecurerootcapabilities=false,
            )
            .file("README.md")
            .contents()
        )
