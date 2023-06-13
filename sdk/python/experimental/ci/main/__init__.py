import dagger
from dagger.server import command, commands

from ._base import Base
from .engine import Engine
from .sdk import SDK


@commands
class CI(Base):
    @command
    async def engine(self) -> Engine:
        return Engine(src_dir=self.src_dir)

    @command
    async def sdk(self) -> SDK:
        return SDK(src_dir=self.src_dir)


@command
async def ci(client: dagger.Client, repo: str, branch: str) -> CI:
    return CI(src_dir=client.git(repo).branch(branch).tree())
