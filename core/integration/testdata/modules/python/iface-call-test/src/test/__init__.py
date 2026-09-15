import typing

import dagger
from dagger import dag


@dagger.interface
class Duck(typing.Protocol):
    @dagger.function
    async def quack(self) -> str: ...


@dagger.object_type
class Test:
    @dagger.function
    def get_duck(self) -> Duck:
        return dag.mallard()
