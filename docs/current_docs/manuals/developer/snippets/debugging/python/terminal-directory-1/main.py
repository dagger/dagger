import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def simple_directory(self) -> str:
        return (
            dag.git("https://github.com/dagger/dagger.git")
            .head()
            .tree()
            .terminal()
            .file("README.md")
            .contents()
        )
