from gql.client import AsyncClientSession

from dagger import Config

from .context import ResourceManager
from .engine import Engine
from .session import Session


class Connection(ResourceManager):
    """
    Connect to a Dagger Engine.

    Example::

        async def main():
            async with dagger.Connection() as client:
                ctr = client.container().from_("alpine")

    You can stream the logs from the engine to see progress::

        import sys
        import anyio
        import dagger

        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.Connection(cfg) as client:
                ctr = client.container().from_("python:3.11.1-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.11.1

        anyio.run(main)
    """

    def __init__(self, config: Config | None = None) -> None:
        super().__init__()
        self.cfg = config or Config()

    async def start(self) -> AsyncClientSession:
        async with self.get_stack() as stack:
            conn = await stack.enter_async_context(Engine(self.cfg))
            return await stack.enter_async_context(Session(conn, self.cfg))

    async def aclose(self) -> None:
        await self.stack.aclose()

    async def __aenter__(self):
        from dagger import Client

        return Client.from_session(await self.start())
