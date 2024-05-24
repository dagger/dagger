import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def set_env(self) -> str:
        """Set environment variables in a container"""
        return await (
            dag.container()
            .from_("alpine")
            .with_(
                self.env_variables(
                    [
					 ("ENV_VAR_1", "VALUE 1"),
					 ("ENV_VAR_2", "VALUE 2"),
					 ("ENV_VAR_3", "VALUE 3")
					]
                )
            )
            .with_exec(["env"])
            .stdout()
        )

    def env_variables(self, envs: list[tuple[str, str]]):
        def env_variables_inner(ctr: dagger.Container):
            for key, value in envs:
                ctr = ctr.with_env_variable(key, value)
            return ctr

        return env_variables_inner
