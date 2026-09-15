import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    async def names(self) -> list[str]:
        return [
            await dag.foo().name(),
            await dag.bar().name(),
        ]
