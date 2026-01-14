"""A module for HelloWithChecksPy functions"""

import dagger
from dagger import dag, check, function, object_type


@object_type
class HelloWithChecksPy:
    baseImage: str = "alpine:3"

    @function
    @check
    async def passing_check(self) -> None:
        """Returns a passing check"""
        await (
            dag.container()
            .from_(self.baseImage)
            .with_exec(["sh", "-c", "exit 0"])
            .sync()
        )

    @function
    @check
    async def failing_check(self) -> None:
        """Returns a failing check"""
        await (
            dag.container()
            .from_(self.baseImage)
            .with_exec(["sh", "-c", "exit 1"])
            .sync()
        )
