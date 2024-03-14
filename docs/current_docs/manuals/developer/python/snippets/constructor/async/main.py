"""An example module using a factory constructor"""
from typing import Annotated

from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    """Functions for testing a project"""

    paralelize: int

    @classmethod
    async def create(
        cls,
        paralelize: Annotated[
            int | None, Doc("Number of parallel processes to run")
        ] = None,
    ):
        if paralelize is None:
            paralelize = int(
                await dag.container().from_("alpine").with_exec(["nproc"]).stdout()
            )
        return cls(paralelize=paralelize)

    @function
    def debug(self) -> str:
        """Check the number of parallel processes"""
        return f"Number of parallel processes: {self.paralelize}"
