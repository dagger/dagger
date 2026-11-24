import contextlib
import logging

from dagger import telemetry
from dagger._managers import ResourceManager
from dagger.client._session import Session, as_session

from ._config import Config
from ._engine import Engine, provision_engine

logger = logging.getLogger(__name__)


class Connection(ResourceManager):
    """Connect to a Dagger Engine with an isolated session (legacy).

    This is an older version of :py:func:`dagger.connection` that uses an
    isolated session instead of the default one. Should no longer be used in
    newer projects unless there's a specific reason to do so.

    Example, with ``core`` the entry function of the generated core client::

        import dagger


        async def main():
            async with dagger.Connection() as session:
                ctr = core(session=session).container().from_("alpine")


    You can stream the logs from the engine to see progress::

        import sys
        import anyio
        import dagger


        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.Connection(cfg) as session:
                ctr = core(session=session).container().from_("python:3.11.1-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.11.1


        anyio.run(main)
    """

    def __init__(self, config: Config | None = None) -> None:
        super().__init__()
        self.cfg = config or Config()

    async def __aenter__(self) -> Session:
        telemetry.initialize()
        logger.debug("Establishing connection with isolated session")
        async with self.get_stack() as stack:
            engine = await Engine(self.cfg, stack).provision()
            session = as_session(engine.get_client_connection())
            await engine.setup_client(session)
            return session

    async def close(self):
        logger.debug("Closing connection with isolated session")
        await super().close()


@contextlib.asynccontextmanager
async def connection(config: Config | None = None):
    """Connect to a Dagger Engine using the default session.

    This is similar to :py:class:`dagger.Connection` but uses the default
    session (:py:attr:`dagger.dag`), the one a client called without
    ``session=`` uses, so there's no need to pass a session around.

    Example, with ``core`` the entry function of the generated core client::

        import dagger


        async def main():
            async with dagger.connection():
                ctr = core().container().from_("alpine")

            # Connection is closed when leaving the context manager's scope.


    You can stream the logs from the engine to see progress::

        import sys
        import anyio
        import dagger


        async def main():
            cfg = dagger.Config(log_output=sys.stderr)

            async with dagger.connection(cfg):
                ctr = core().container().from_("python:3.11.1-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.11.1


        anyio.run(main)
    """
    telemetry.initialize()
    logger.debug("Establishing connection with the default session")
    async with provision_engine(config or Config()) as engine:
        session = as_session(engine.get_shared_client_connection())
        await engine.setup_client(session)
        yield session
        logger.debug("Closing connection with the default session")
