import logging
import os
from abc import ABC, abstractmethod
from collections import UserDict
from pathlib import Path
from typing import TypeVar
from urllib.parse import ParseResult as ParsedURL
from urllib.parse import urlparse

from attrs import Factory, define, field
from gql.client import Client as GraphQLClient
from gql.transport import AsyncTransport, Transport

from ..api import Client

logger = logging.getLogger(__name__)


DEFAULT_HOST = "http://localhost:8080"
DEFAULT_CONFIG = "dagger.json"


@define
class Config:
    """Options for connecting to the Dagger engine.

    Parameters
    ----------
    host:
        Address to connect to the engine.
    workdir:
        The host workdir loaded into dagger.
    config_path:
        Project config file.
    timeout:
        Timeout in seconds for establishing a connection to the server.
    """

    host: ParsedURL = field(factory=lambda: os.environ.get("DAGGER_HOST", DEFAULT_HOST), converter=urlparse)
    timeout: int = 10
    # FIXME: aren't these environment variables usable by the engine directly?
    # If so just skip setting the CLI option if not set by the user explicitly.
    workdir: Path = field(factory=lambda: os.environ.get("DAGGER_WORKDIR", "."), converter=Path)
    config_path: Path = field(factory=lambda: os.environ.get("DAGGER_CONFIG", DEFAULT_CONFIG), converter=Path)


@define
class Connector(ABC):
    """Facilitates instantiating a client and possibly provisioning the engine for it."""

    cfg: Config = Factory(Config)
    client: Client | None = None

    async def connect(self) -> Client:
        transport = self.make_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = await gql_client.connect_async(reconnecting=True)
        self.client = Client.from_session(session)
        return self.client

    async def close(self) -> None:
        assert self.client is not None
        await self.client._gql_client.close_async()

    def connect_sync(self) -> Client:
        transport = self.make_sync_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = gql_client.connect_sync()
        self.client = Client.from_session(session)
        return self.client

    def close_sync(self) -> None:
        assert self.client is not None
        self.client._gql_client.close_sync()

    @abstractmethod
    def make_transport(self) -> AsyncTransport:
        ...

    @abstractmethod
    def make_sync_transport(self) -> Transport:
        ...

    def make_graphql_client(self, transport: AsyncTransport | Transport) -> GraphQLClient:
        return GraphQLClient(transport=transport, fetch_schema_from_transport=True)


_RT = TypeVar("_RT", bound=type)


class _Registry(UserDict[str, type[Connector]]):
    def add(self, scheme: str):
        def register(cls: _RT) -> _RT:
            if scheme not in self.data:
                if not issubclass(cls, Connector):
                    raise TypeError(f"{cls.__name__} isn't a Connector subclass")
                self.data[scheme] = cls
            elif cls is not self.data[scheme]:
                raise TypeError(f"Can't re-register {scheme} connector: {cls.__name__}")
            else:
                # FIXME: make sure imports don't create side effect of registering multiple times
                logger.debug(f"Attempted to re-register {scheme} connector")
            return cls

        return register

    def get_(self, cfg: Config) -> Connector:
        try:
            cls = self.data[cfg.host.scheme]
        except KeyError:
            raise ValueError(f'Invalid dagger host "{cfg.host.geturl()}"')
        return cls(cfg)


_registry = _Registry()


def register_connector(schema: str):
    return _registry.add(schema)


def get_connector(cfg: Config) -> Connector:
    return _registry.get_(cfg)
