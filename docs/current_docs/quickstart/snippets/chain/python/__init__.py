import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    def foo(self) -> dagger.Container:
        """Returns a container"""
        return dag.container().from_("cgr.dev/chainguard/wolfi-base")

    @function
    async def bar(self) -> str:
        """Publishes a container"""
        return await self.foo().publish("ttl.sh/bar")
