import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def base(self) -> dagger.Container:
        """Return a container"""
        return (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["mkdir", "/src"])
            .with_exec(["touch", "/src/foo", "/src/bar"])
        )
