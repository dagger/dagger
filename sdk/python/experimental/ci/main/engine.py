from dagger.server import command, commands

from ._base import Base


@commands
class Engine(Base):
    @command
    async def test(self) -> str:
        return "engine test"
