import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def build(self, source: dagger.Directory) -> dagger.Directory:
        """Create a production build"""
        return (
            dag.node(ctr=self.build_base_image(source))
            .build()
            .container()
            .directory("./dist")
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Run unit tests"""
        return await (
            dag.node(ctr=self.build_base_image(source))
            .commands()
            .run(["test:unit", "run"])
            .stdout()
        )

    def build_base_image(self, source: dagger.Directory) -> dagger.Container:
        """Build base image"""
        return (
            dag.node(version="21").with_npm().with_source(source).install().container()
        )
