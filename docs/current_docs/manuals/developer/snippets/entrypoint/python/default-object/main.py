from typing import Annotated

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    ctr: dagger.Container = dag.container().from_("alpine:3.14.0")

    @function
    async def version(self) -> str:
        return await self.ctr.with_exec(
            ["/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"]
        ).stdout()
