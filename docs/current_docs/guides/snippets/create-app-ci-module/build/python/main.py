import random
import dagger
from dagger import dag, object_type, function

@object_type
class MyModule:
    @function
    def build(self, source: dagger.Directory) -> dagger.Directory:
        """Create a production build"""
        return (
            dag.node().with_container(self.build_base_image(source))
            .build()
            .container()
            .directory("./dist")
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Run unit tests"""
        return await (
            dag.node().with_container(self.build_base_image(source))
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