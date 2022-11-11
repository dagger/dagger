import logging
import os
from abc import ABC, abstractmethod
from collections import UserDict
from pathlib import Path
from typing import TextIO, TypeVar
from urllib.parse import ParseResult as ParsedURL
from urllib.parse import urlparse

from attrs import Factory, define, field
from gql.client import Client as GraphQLClient
from gql.transport import AsyncTransport, Transport

from dagger import Client, SyncClient

from .engine_version import ENGINE_IMAGE_REF

logger = logging.getLogger(__name__)

DEFAULT_HOST = f"docker-image://{ENGINE_IMAGE_REF}"


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
    log_output:
        A TextIO object to send the logs from the engine.
    timeout:
        The maximum time in seconds for establishing a connection to the server.
    execute_timeout:
        The maximum time in seconds for the execution of a request before a TimeoutError
        is raised. Only used for async transport.
        Passing None results in waiting forever for a response.
    reconnecting:
        If True, create a permanent reconnecting session. Only used for async transport.
    """

    host: ParsedURL = field(
        factory=lambda: os.environ.get("DAGGER_HOST", DEFAULT_HOST),
        converter=urlparse,
    )
    workdir: Path | str = ""
    config_path: Path | str = ""
    log_output: TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = 60 * 5
    reconnecting: bool = True


@define
class Connector(ABC):
    """Facilitates instantiating a client and possibly
    provisioning the engine for it.
    """

    cfg: Config = Factory(Config)
    client: Client | SyncClient | None = None

    async def connect(self) -> Client:
        transport = self.make_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = await gql_client.connect_async(reconnecting=self.cfg.reconnecting)
        self.client = Client.from_session(session)
        return self.client

    async def close(self, exc_type) -> None:
        assert self.client is not None
        await self.client._gql_client.close_async()

    def connect_sync(self) -> SyncClient:
        transport = self.make_sync_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = gql_client.connect_sync()
        self.client = SyncClient.from_session(session)
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

    def make_graphql_client(
        self, transport: AsyncTransport | Transport
    ) -> GraphQLClient:
        return GraphQLClient(
            transport=transport,
            fetch_schema_from_transport=True,
            execute_timeout=self.cfg.execute_timeout,
        )


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
                # FIXME: make sure imports don't create side effect of registering
                # multiple times
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
