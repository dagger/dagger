import random

import dagger
from dagger import dag, function, object_type


@object_type
class HelloDagger:
    @function
    async def publish(self, source: dagger.Directory) -> str:
        """Publish the application container after building and testing it on-the-fly"""
        self.test(source)
        return await self.build(source).publish(
            f"ttl.sh/myapp-{random.randrange(10 ** 8)}"
        )

    @function
    def build(self, source: dagger.Directory) -> dagger.Container:
        """Build the application container"""
        build = (
            self.build_env(source)
            .with_exec(["npm", "run", "build"])
            .directory("./dist")
        )
        return (
            dag.container()
            .from_("nginx:1.25-alpine")
            .with_directory("/usr/share/nginx/html", build)
            .with_exposed_port(8080)
        )

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Return the result of running unit tests"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )

    @function
    def build_env(self, source: dagger.Directory) -> dagger.Container:
        """Build a ready-to-use development environment"""
        node_cache = dag.cache_volume("node")
        return (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", source)
            .with_mounted_cache("/src/node_modules", node_cache)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
        )
