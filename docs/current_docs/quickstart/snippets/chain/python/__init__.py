import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    def foo(self) -> dagger.Directory:
        return dag.container().from_("cgr.dev/chainguard/wolfi-base").directory("/")

    @function
    async def bar(self) -> list[str]:
        return await self.foo().entries()
