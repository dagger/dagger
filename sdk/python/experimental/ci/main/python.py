import dagger
from dagger.server import command, commands

from ._base import Base


@commands
class Python(Base):
    @command
    async def host(self, dag: dagger.Client) -> str:
        return "\n".join(await dag.host().directory(".").entries())

    @command
    async def lint(self, dag: dagger.Client) -> str:
        return await (
            dag.container()
            .from_("alpine")
            .with_mounted_directory("/src", dag.host().directory("."))
            .with_exec(["ls", "-lha", "/src"])
            .stdout()
        )

    @command
    def test(self, client: dagger.Client) -> dagger.Directory:
        return client.directory().with_file(
            "README2.md",
            client.host().directory(".").file("README.md"),
        )
