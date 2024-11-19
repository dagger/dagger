import anyio

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    source: dagger.Directory

    @function
    async def test(self) -> str:
        """Return the result of running unit tests"""
        return await (
            self.build_env().with_exec(["npm", "run", "test:unit", "run"]).stdout()
        )

    @function
    async def typecheck(self) -> str:
        """Return the result of running the type checker"""
        return await self.build_env().with_exec(["npm", "run", "type-check"]).stdout()

    @function
    async def lint(self) -> str:
        """Return the result of running the linter"""
        return await self.build_env().with_exec(["npm", "run", "lint"]).stdout()

    @function
    async def run_all_tests(self):
        """Run linter, type-checker, unit tests concurrently"""
        async with anyio.create_task_group() as tg:
            tg.start_soon(self.lint)
            tg.start_soon(self.typecheck)
            tg.start_soon(self.test)

    @function
    def build_env(self) -> dagger.Container:
        """Build a ready-to-use development environment"""
        node_cache = dag.cache_volume("node")
        return (
            dag.container()
            .from_("node:21-slim")
            .with_directory("/src", self.source)
            .with_mounted_cache("/root/.npm", node_cache)
            .with_workdir("/src")
            .with_exec(["npm", "install"])
        )
