import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def get_dir(self) -> dagger.Directory:
        """Return a directory"""
        return self.base().directory("/src")

    @function
    def get_file(self) -> dagger.File:
        """Return a file"""
        return self.base().file("/src/foo")

    @function
    def base(self) -> dagger.Container:
        """Return a base container"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["mkdir", "/src"])
            .with_exec(["touch", "/src/foo", "/src/bar"])
        )
