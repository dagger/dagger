"""Module to test a runtime module extension

This module tests adding a dependency using git, through a custom runtime
module extension that adds the git binary.
"""

import anyio
import asyncer

from dagger import dag, function, object_type


@object_type
class GitDep:
    @function
    async def echo(self, msg: str) -> str:
        return await (
            dag.container()
            .from_("alpine")
            .with_exec(["echo", msg])
            .stdout()
        )

    @function
    async def git_version(self) -> str:
        r = await anyio.run_process(["git", "--version"])
        return r.stdout.decode()

    @function
    async def hello(self) -> str:
        async with asyncer.create_task_group() as tg:
            p = tg.soonify(self.echo)(msg="voilà")
            v = tg.soonify(self.git_version)()

        return f"{p.value.strip()}: {v.value.strip()}"
