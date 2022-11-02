import logging
from typing import NoReturn

from .api import Client
from .connectors import Config, get_connector

logger = logging.getLogger(__name__)


class Connection:
    """Connect to a Dagger Engine."""

    def __init__(self, config: Config = None) -> None:
        if config is None:
            config = Config()
        self.connector = get_connector(config)

    async def __aenter__(self) -> Client:
        return await self.connector.connect()

    async def __aexit__(self, *args, **kwargs) -> None:
        await self.connector.close()

    def __enter__(self) -> NoReturn:
        raise NotImplementedError("Sync is not supported yet. Use `async with` instead.")

    def __exit__(self, *args, **kwargs) -> None:
        ...
