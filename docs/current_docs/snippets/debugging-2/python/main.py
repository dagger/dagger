import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def foo(self) -> dagger.Container:
        return await (
            dag.container()
            .from_("alpine:latest")
            .terminal()
            .with_exec(["sh", "-c", "echo hello world > /foo"])
            .terminal()
        )
