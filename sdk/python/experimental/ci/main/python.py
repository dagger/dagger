import dagger
from dagger.server import command, commands

from ._base import Base


@commands
class Python(Base):
    @command
    async def lint(self) -> str:
        return "python lint"

    @command
    async def test(self, client: dagger.Client) -> dagger.File:
        return (
            client.directory()
            .with_file("README2.md", client.host().directory("/src").file("README.md"))
            .file("README2.md")
        )
