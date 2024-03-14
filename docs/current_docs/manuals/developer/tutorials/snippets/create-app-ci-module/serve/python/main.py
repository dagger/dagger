import random

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def serve(self, source: dagger.Directory) -> dagger.Service:
        """Create a service from the production image"""
        return self.package(source).as_service()

    @function
    async def publish(self, source: dagger.Directory) -> str:
        """Publish an image"""
        return await self.package(source).publish(
            f"ttl.sh/myapp-{random.randrange(10 ** 8)}"
        )

    def package(self, source: dagger.Directory) -> dagger.Container:
        """Create a production image"""
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", self.build(source))
            .with_exposed_port(80)
        )

    @function
    def build(self, source: dagger.Directory) -> dagger.Directory:
        """Create a production build"""
        return (
            dag.node()
            .with_container(self.build_base_image(source))
            .build()
            .container()
            .directory("./dist")
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Run unit tests"""
        return await (
            dag.node()
            .with_container(self.build_base_image(source))
            .run(["run", "test:unit", "run"])
            .stdout()
        )

    def build_base_image(self, source: dagger.Directory) -> dagger.Container:
        """Build base image"""
        return (
            dag.node()
            .with_version("21")
            .with_npm()
            .with_source(source)
            .install([])
            .container()
        )
