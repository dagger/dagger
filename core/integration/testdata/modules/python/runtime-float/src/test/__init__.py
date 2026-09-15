import dagger
from dagger import dag


@dagger.object_type
class Test:
    @dagger.function
    def test(self, n: float) -> float:
        return n

    @dagger.function
    def testFloat32(self, n: float) -> float:
        return n

    @dagger.function
    async def dep(self, n: float) -> float:
        return await dag.dep().dep(n)
