from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def set_env_var(self) -> str:
        """Set a single environment variable in a container"""
        return await (
            dag.container()
            .from_("alpine")
            .with_env_variable("ENV_VAR", "VALUE")
            .with_exec(["env"])
            .stdout()
        )
