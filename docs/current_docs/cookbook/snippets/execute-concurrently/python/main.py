import anyio

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:

    @function
    async def test(self, source: dagger.Directory) -> str:
        """Return the result of running unit tests"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "test:unit", "run"])
            .stdout()
        )

    @function
    async def typecheck(self, source: dagger.Directory) -> str:
        """Return the result of running the type checker"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "type-check"])
            .stdout()
        )

    @function
    async def lint(self, source: dagger.Directory) -> str:
        """Return the result of running the linter"""
        return await (
            self.build_env(source)
            .with_exec(["npm", "run", "lint"])
            .stdout()
        )

    @function
    async def run_all_tests(self, source: dagger.Directory) -> str:
        """Run linter, type-checker, unit tests concurrently"""
        # create task group
        async with anyio.create_task_group() as tg:
            lint_op = tg.start_soon(self.lint, source)
            typecheck_op = tg.start_soon(self.typecheck, source)
            test_op = tg.start_soon(self.test, source)

        # wait for all tests to complete
        lint_result = await lint_op
        typecheck_result = await typecheck_op
        test_result = await test_op

        # if all tests succeed, print the test results
        return lint_result + "\n" + typecheck_result + "\n" + test_result

    @function
    def build_env(self, source: dagger.Directory) -> dagger.Container:
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
