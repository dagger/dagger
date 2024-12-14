import random

from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(self) -> str:
        """Build and publish image with OCI annotations"""
        address = await (
            dag.container()
            .from_("alpine:latest")
            .with_exec(["apk", "add", "git"])
            .with_workdir("/src")
            .with_exec(["git", "clone", "https://github.com/dagger/dagger", "."])
            .with_annotation("org.opencontainers.image.authors", "John Doe")
            .with_annotation(
                "org.opencontainers.image.title", "Dagger source image viewer"
            )
            .publish(f"ttl.sh/custom-image-{random.randrange(10 * 7)}")
        )
        return address
