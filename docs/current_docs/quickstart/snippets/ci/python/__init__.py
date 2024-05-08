import random
import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    async def ci(self, source: dagger.Directory) -> str:
        """Tests, builds, packages and publishes the application"""
        # run tests
        self.test(source)
        # obtain the build output directory
        build = self.build(source)
        # create and publish a container with the build output
        return await self.package(build).publish(
            f"ttl.sh/myapp-{random.randrange(10 ** 8)}"
        )

    @function
    def package(self, build: dagger.Directory) -> dagger.Container:
        """Returns a container with the production build"""
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", build)
            .with_exposed_port(80)
        )

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
