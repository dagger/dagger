from gql.client import AsyncClientSession

from dagger import Client, Config

from ._progress import Progress
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
            progress = await stack.enter_async_context(Progress(self.cfg.console))
            conn = await stack.enter_async_context(Engine(self.cfg, progress))

            progress.update("Establishing connection to Engine")
            session = await stack.enter_async_context(Session(conn, self.cfg))

            # If log_output is set, we don't need to show any more progress.
            if self.cfg.log_output:
                progress.stop()
            else:
                progress.update("Running pipelines")

            return session

    async def aclose(self) -> None:
        await self.stack.aclose()

    async def __aenter__(self):
        return Client.from_session(await self.start())
