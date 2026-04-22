import dagger
from dagger import dag


@dagger.object_type
class Usage:
    @dagger.function
    async def test(self) -> str:
        return await dag.my_module().foo(dag.example().as_my_module_fooer())
