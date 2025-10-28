import dagger
from dagger import dag, function, object_type


@object_type
class MyblueprintPy:
    @function
    async def hello(self) -> str:
        """Returns the string 'hello from blueprint'"""
        return (
            await dag.container()
            .from_("alpine:latest")
            .with_exec(["echo", "hello from blueprint"])
            .stdout()
        )
