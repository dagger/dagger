import random
import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:
    source: dagger.Directory = field()

    @classmethod
    def create(cls, source: dagger.Directory):
        return cls(source=source)

    # create a service from the production image
    @function
    def serve(self) -> dagger.Service:
        return self.package().as_service()

    # publish an image
    @function
    async def publish(self) -> str:
        return await (
            self.package()
            .publish(f"ttl.sh/myapp-{random.randrange(10 ** 8)}")
        )

    # create a production image
    def package(self) -> dagger.Container:
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", self.build())
            .with_exposed_port(80)
        )

    # create a production build
    @function
    def build(self) -> dagger.Directory:
        return (
            dag.node().with_container(self.build_base_image())
            .build()
            .container()
            .directory("./dist")
        )

    # run unit tests
    @function
    async def test(self) -> str:
        return await (
            dag.node().with_container(self.build_base_image())
            .run(["run", "test:unit", "run"])
            .stdout()
        )

    # build base image
    def build_base_image(self) -> dagger.Container:
        return (
            dag.node()
            .with_version("21")
            .with_npm()
            .with_source(self.source)
            .install([])
            .container()
        )
