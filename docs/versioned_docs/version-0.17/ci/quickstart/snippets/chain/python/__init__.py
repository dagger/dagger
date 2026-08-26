import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    def base(self) -> dagger.Container:
        """Returns a base container"""
        return dag.container().from_("cgr.dev/chainguard/wolfi-base")

    @function
    def build(self) -> dagger.Container:
        """Builds on top of base container and returns a new container"""
        return self.base().with_exec(["apk", "add", "bash", "git"])

    @function
    async def build_and_publish(self) -> str:
        """Builds and publishes a container"""
        return await self.build().publish("ttl.sh/bar")
