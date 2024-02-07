import random
import dagger
from dagger import dag, object_type, field, function

@object_type
class MyModule:
    source: dagger.Directory = field()

    @classmethod
    def create(cls, source: dagger.Directory):
        return cls(source=source)

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
