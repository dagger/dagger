import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def show_secret(
        self,
        token: dagger.Secret,
    ) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_secret_variable("MY_SECRET", token)
            .with_exec(
                [
                    "sh",
                    "-c",
                    ("echo this is the secret: $MY_SECRET"),
                ]
            )
            .stdout()
        )
