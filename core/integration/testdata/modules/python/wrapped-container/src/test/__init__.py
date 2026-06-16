from typing import Self

import dagger
from dagger import dag


@dagger.object_type
class WrappedContainer:
    unwrap: dagger.Container = dagger.field()

    @dagger.function
    def echo(self, msg: str) -> Self:
        return WrappedContainer(unwrap=self.unwrap.with_exec(["echo", "-n", msg]))


@dagger.object_type
class Test:
    @dagger.function
    def container(self) -> WrappedContainer:
        return WrappedContainer(unwrap=dag.container().from_("alpine:3.22.1"))
