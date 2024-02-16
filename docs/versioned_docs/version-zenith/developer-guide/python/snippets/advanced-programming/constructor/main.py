"""A Dagger module for searching an input file."""
import dagger
from dagger import dag, object_type, field, function


@object_type
class Grep:
    src: dagger.File = field()

    @classmethod
    async def create(cls, src: dagger.File | None = None):
        if src is None:
            src = await dag.http("https://dagger.io")
        return cls(src=src)

    @function
    async def grep(self, pattern: str) -> str:
        return await (
            dag
            .container()
            .from_("alpine:latest")
            .with_mounted_file("/src", self.src)
            .with_exec(["grep", pattern, "/src"])
            .stdout()
        )
