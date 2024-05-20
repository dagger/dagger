import random

import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    async def publish(self, source: dagger.Directory) -> str:
        """Tests, builds and publishes the application"""
        self.test(source)
        return await self.build(source).publish(
            f"ttl.sh/hello-dagger-{random.randrange(10 ** 8)}"
        )

    @function
    def build(self, source: dagger.Directory) -> dagger.Container:
        """Returns a container with the production build and an NGINX service"""
        build = (
            dag.node(ctr=self.build_env(source))
            .commands()
            .run(["build"])
            .directory("./dist")
        )
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", build)
            .with_exposed_port(80)
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Returns the result of running unit tests"""
        return await (
            dag.node(ctr=self.build_env(source))
            .commands()
            .run(["test:unit", "run"])
            .stdout()
        )

    @function
    def build_env(self, source: dagger.Directory) -> dagger.Container:
        """Returns a container with the build environment"""
        return (
            dag.node(version="21").with_npm().with_source(source).install().container()
        )
