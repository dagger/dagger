"""A module for HelloWithChecksPy functions"""

import dagger
from dagger import check, dag, function, object_type


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

    @function
    @check
    def passing_container(self) -> dagger.Container:
        """Returns a container which runs as a passing check"""
        return dag.container().from_(self.baseImage).with_exec(["sh", "-c", "exit 0"])

    @function
    @check
    def failing_container(self) -> dagger.Container:
        """Returns a container which runs as a failing check"""
        return dag.container().from_(self.baseImage).with_exec(["sh", "-c", "exit 1"])
