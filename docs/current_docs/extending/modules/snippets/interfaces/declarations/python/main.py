import typing

import dagger


@dagger.interface
class Fooer(typing.Protocol):
    @dagger.function
    async def foo(self, bar: int) -> str: ...


@dagger.object_type
class MyModule:
    @dagger.function
    async def foo(self, fooer: Fooer) -> str:
        return await fooer.foo(42)
