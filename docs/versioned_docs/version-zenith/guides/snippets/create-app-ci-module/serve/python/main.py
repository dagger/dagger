import random
import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:
    source: dagger.Directory = field()

    @function
    def serve(self) -> dagger.Service:
        """Create a service from the production image"""
        return self.package().as_service()

    @function
    async def publish(self) -> str:
        """Publish an image"""
        return await (
            self.package()
            .publish(f"ttl.sh/myapp-{random.randrange(10 ** 8)}")
        )

    def package(self) -> dagger.Container:
        """Create a production image"""
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", self.build())
            .with_exposed_port(80)
        )

    @function
    def build(self) -> dagger.Directory:
        """Create a production build"""
        return (
            dag.node().with_container(self.build_base_image())
            .build()
            .container()
            .directory("./dist")
        )

    @function
    async def test(self) -> str:
        """Run unit tests"""
        return await (
            dag.node().with_container(self.build_base_image())
            .run(["run", "test:unit", "run"])
            .stdout()
        )

    def build_base_image(self) -> dagger.Container:
        """Build base image"""
        return (
            dag.node()
            .with_version("21")
            .with_npm()
            .with_source(self.source)
            .install([])
            .container()
        )
