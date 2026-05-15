import dataclasses

import dagger
from dagger import dag, DepStatus

@dagger.object_type
class Test:
    status: DepStatus = dataclasses.field(default=DepStatus.ACTIVE, init=False)

    @dagger.function
    def active(self) -> str:
        return str(self.status)

    @dagger.function
    async def inactive(self) -> str:
        status = await dag.dep().active()
        status = await dag.dep().invert(status)
        return str(status)
