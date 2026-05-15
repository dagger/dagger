import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    async def use_hello(self) -> str:
        return await dag.dep().hello()
