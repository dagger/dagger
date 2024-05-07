import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def write_directory(self, dir: dagger.Directory) -> dagger.Container:
        """Returns a container with a specified directory"""
        return dag.container().from_("alpine:latest").with_directory("/src", dir)
