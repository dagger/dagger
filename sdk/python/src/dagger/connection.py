from dagger import Client, Config, SyncClient

from .context import ResourceManager, SyncResourceManager
from .engine import Engine
from .session import Session


class Connection(ResourceManager, SyncResourceManager):
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

    async def __aenter__(self) -> Client:
        async with self.get_stack() as stack:
            conn = await stack.enter_async_context(Engine(self.cfg))
            session = await stack.enter_async_context(Session(conn, self.cfg))
        return Client.from_session(session)

    def __enter__(self) -> SyncClient:
        with self.get_sync_stack() as stack:
            conn = stack.enter_context(Engine(self.cfg))
            session = stack.enter_context(Session(conn, self.cfg))
        return SyncClient.from_session(session)
