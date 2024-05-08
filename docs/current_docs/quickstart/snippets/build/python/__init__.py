import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    def build(self, source: dagger.Directory) -> dagger.Directory:
        """Returns a directory with the production build"""
        return (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", source.without_directory("dagger"))
            .with_workdir("/src")
            .with_exec(["npm", "install"])
            .with_exec(["npm", "run", "build"])
            .directory("./dist")
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Returns the result of running unit tests"""
        return await (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", source.without_directory("dagger"))
            .with_workdir("/src")
            .with_exec(["npm", "install"])
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )
