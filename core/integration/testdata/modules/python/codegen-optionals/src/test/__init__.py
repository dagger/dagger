import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    async def test(self) -> str:
        return await dag.dep().ctl("foo")
