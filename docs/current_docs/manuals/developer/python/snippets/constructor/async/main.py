"""An example module using a factory constructor"""
from typing import Annotated

from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    """Functions for testing a project"""

    parallelize: int

    @classmethod
    async def create(
        cls,
        parallelize: Annotated[
            int | None, Doc("Number of parallel processes to run")
        ] = None,
    ):
        if parallelize is None:
            parallelize = int(
                await dag.container().from_("alpine").with_exec(["nproc"]).stdout()
            )
        return cls(parallelize=parallelize)

    @function
    def debug(self) -> str:
        """Check the number of parallel processes"""
        return f"Number of parallel processes: {self.parallelize}"
