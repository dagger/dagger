import random
from typing import Annotated

import dagger
from dagger import DefaultPath, dag, function, object_type


@object_type
class HelloDagger:
    @function
    async def publish(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> str:
        """Publish the application container after building and testing it on-the-fly"""
        await self.test(source)
        return await self.build(source).publish(
            f"ttl.sh/hello-dagger-{random.randrange(10**8)}"
        )

    @function
    def build(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> dagger.Container:
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
            .with_exposed_port(80)
        )

    @function
    async def test(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> str:
        """Return the result of running unit tests"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )

    @function
    def build_env(
        self,
        source: Annotated[dagger.Directory, DefaultPath("/")],
    ) -> dagger.Container:
        """Build a ready-to-use development environment"""
        node_cache = dag.cache_volume("node")
        return (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", source)
            .with_mounted_cache("/root/.npm", node_cache)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
        )
