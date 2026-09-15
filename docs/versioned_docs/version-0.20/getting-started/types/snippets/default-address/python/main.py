from typing import Annotated

import dagger
from dagger import DefaultAddress, function, object_type


@object_type
class MyModule:
    @function
    async def version(
        self,
        ctr: Annotated[dagger.Container, DefaultAddress("alpine:latest")],
    ) -> str:
        return await ctr.with_exec(["cat", "/etc/alpine-release"]).stdout()
