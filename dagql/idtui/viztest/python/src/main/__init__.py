import datetime

from dagger import dag, function, object_type

now = str(datetime.datetime.now())

@object_type
class Python:
    @function
    async def echo(self, msg: str) -> str:
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["echo", msg])
            .stdout()
        )

    @function
    async def custom_span(self) -> str:
        async with dag.span("custom span"):
            return await self.echo(f"hello from Python! it is currently {now}")

    @function
    async def exceptional_span(self) -> str:
        async with dag.span("custom span"):
            raise ValueError("oh no")

    @function
    async def pending(self):
        return await (
            dag.container()
            .from_("alpine:latest")
            .with_env_variable("NOW", now)
            .with_exec(["sleep", "1"])
            .with_exec(["false"])
            .with_exec(["sleep", "1"])
            .sync()
        )
