import random
import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:
    source: dagger.Directory = field()

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
