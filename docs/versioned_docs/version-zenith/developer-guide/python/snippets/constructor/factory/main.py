
"""An example module using default factory functions."""
import dataclasses

import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    base: dagger.Container = dataclasses.field(
        default_factory=lambda: dag.container().from_("python:alpine"),
    )
    packages: list[str] = dataclasses.field(default_factory=list)

    @function
    def container(self) -> dagger.Container:
        return self.base.with_exec(["apk", "add", "git", *self.packages])
