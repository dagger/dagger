from dagger.server import command, commands

from ._base import Base


@commands
class Go(Base):
    @command
    async def build(self) -> str:
        return "go build"
