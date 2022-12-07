from .connector import Config, Connector
from .engine import get_engine


class Connection:
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
                ctr = client.container().from_("python:3.10.8-alpine")
                version = await ctr.with_exec(["python", "-V"]).stdout()

            print(version)
            # Output: Python 3.10.8

        anyio.run(main)
    """

    def __init__(self, config: Config = None) -> None:
        if config is None:
            config = Config()
        self.engine = get_engine(config)
        self.connector = Connector(config)

    async def __aenter__(self):
        # FIXME: handle cancellation, retries and timeout properly
        # FIXME: handle errors during provisioning
        await self.engine.__aenter__()
        return await self.connector.__aenter__()

    async def __aexit__(self, *args, **kwargs) -> None:
        # FIXME: need exit stack?
        await self.connector.__aexit__(*args, **kwargs)
        await self.engine.__aexit__(*args, **kwargs)

    def __enter__(self):
        self.engine.__enter__()
        return self.connector.__enter__()

    def __exit__(self, *args, **kwargs) -> None:
        self.connector.__exit__(*args, **kwargs)
        self.engine.__exit__(*args, **kwargs)
