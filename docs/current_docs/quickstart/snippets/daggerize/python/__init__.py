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
            f"ttl.sh/myapp-{random.randrange(10 ** 8)}"
        )

    @function
    def build(self, source: dagger.Directory) -> dagger.Container:
        """Returns a container with the production build and an NGINX service"""
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
        """Returns the result of running unit tests"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )

    @function
    def build_env(self, source: dagger.Directory) -> dagger.Container:
        """Returns a container with the build environment"""
        node_cache = dag.cache_volume("node")
        return (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", source)
            .with_mounted_cache("/src/node_modules", node_cache)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
        )
