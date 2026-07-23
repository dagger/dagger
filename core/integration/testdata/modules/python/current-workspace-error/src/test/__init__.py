import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    async def fn(self) -> str:
        return await dag.current_workspace().cwd()
