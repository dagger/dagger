import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def copy_directory(self, d: dagger.Directory) -> dagger.Container:
        """Returns a container with a specified directory"""
        return dag.container().from_("alpine:latest").with_directory("/src", d)
