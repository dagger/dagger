import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def container(self) -> dagger.Container:
        return (
            dag.container()
            .from_("alpine:latest")
            .terminal()
            .with_exec(["sh", "-c", "echo hello world > /foo && cat /foo"])
            .terminal()
        )
