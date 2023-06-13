from dagger.server import command, commands

from ._base import Base
from .go import Go
from .python import Python


@commands
class SDK(Base):
    @command
    async def go(self) -> Go:
        return Go(src_dir=self.src_dir)

    @command
    async def python(self) -> Python:
        return Python(src_dir=self.src_dir)
