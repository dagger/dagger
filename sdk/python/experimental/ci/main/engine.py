import dagger
from dagger.server import command, commands

from ._base import Base


@commands
class Engine(Base):
    @command
    async def build(self, client: dagger.Client) -> dagger.Directory:
        return client.directory().with_new_file("readme.md", "Hello builders!")

    @command
    async def test(self, client: dagger.Client) -> dagger.Directory:
        return client.directory().with_new_file("readme.md", "Hello world!")
