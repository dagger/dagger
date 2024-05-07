import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def modify_directory(self, dir: dagger.Directory) -> dagger.Container:
        """Returns a container with a specified directory and an additional file"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_directory("/src", dir)
            .with_exec(["/bin/sh", "-c", "`echo foo > /src/foo`"])
        )
