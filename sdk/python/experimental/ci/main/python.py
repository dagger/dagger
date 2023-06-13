from dagger.server import command, commands

from ._base import Base


@commands
class Python(Base):
    @command
    async def lint(self) -> str:
        return "python lint"

    @command
    async def test(self) -> str:
        return "python test"
