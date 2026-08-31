from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def foo(self) -> str:
        return (
            await dag.container()
            .from_("alpine:latest")
            .with_entrypoint(["cat", "/etc/os-release"])
            .publish("ttl.sh/my-alpine")
        )
