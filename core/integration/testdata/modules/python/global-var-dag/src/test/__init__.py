from dagger import dag, function, object_type


SOME_DEFAULT = dag.container().from_("alpine:3.22.1")


@object_type
class Test:
    @function
    async def fn(self) -> str:
        return await SOME_DEFAULT.with_exec(["echo", "foo"]).stdout()
