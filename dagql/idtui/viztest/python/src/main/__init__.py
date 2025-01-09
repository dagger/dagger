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
    async def nested_spans(self, fail: bool = False) -> str:
        async with dag.span("custom span"):
            await self.echo(f"outer: {now}")

            async with dag.span("sub span"):
                await self.echo(f"sub 1: {now}")

            async with dag.span("sub span"):
                await self.echo(f"sub 2: {now}")

            async with dag.span("another sub span"):
                async with dag.span("sub span"):
                    if fail:
                        raise ValueError("oh no")
                    else:
                        await self.echo(f"im even deeper: {now}")

        return "done"

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
