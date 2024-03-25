"""An example module controlling constructor parameters."""
import dataclasses

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    base: dagger.Container = dataclasses.field(init=False)
    variant: dataclasses.InitVar[str] = "alpine"

    def __post_init__(self, variant: str):
        self.base = dag.container().from_(f"python:{variant}")

    @function
    def container(self) -> dagger.Container:
        return self.base
