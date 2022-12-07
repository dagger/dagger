import logging
from typing import TypeVar

from attrs import Factory, define
from gql.client import Client as GraphQLClient
from gql.transport import AsyncTransport, Transport

from dagger import Client, Config, SyncClient

from .transport.httpx import HTTPXAsyncTransport, HTTPXTransport

logger = logging.getLogger(__name__)


_T = TypeVar("_T")


@define
class Connector:
    """Facilitates connecting to the engine."""

    cfg: Config = Factory(Config)
    client: Client | SyncClient | None = None

    @property
    def query_url(self) -> str:
        return f"{self.cfg.host.geturl()}"

    async def connect(self) -> Client:
        transport = self.make_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = await gql_client.connect_async(reconnecting=self.cfg.reconnecting)
        self.client = Client.from_session(session)
        return self.client

    async def close(self, exc_type) -> None:
        if self.client:
            await self.client._gql_client.close_async()
            self.client = None

    def connect_sync(self) -> SyncClient:
        transport = self.make_sync_transport()
        gql_client = self.make_graphql_client(transport)
        # FIXME: handle errors from establishing session
        session = gql_client.connect_sync()
        self.client = SyncClient.from_session(session)
        return self.client

    def close_sync(self, exc_type) -> None:
        if self.client:
            self.client._gql_client.close_sync()
            self.client = None

    def make_transport(self) -> AsyncTransport:
        return self._make_transport(HTTPXAsyncTransport)

    def make_sync_transport(self) -> Transport:
        return self._make_transport(HTTPXTransport)

    def _make_transport(self, cls: type[_T]) -> _T:
        return cls(self.query_url, timeout=self.cfg.execute_timeout)

    def make_graphql_client(
        self, transport: AsyncTransport | Transport
    ) -> GraphQLClient:
        return GraphQLClient(
            transport=transport,
            fetch_schema_from_transport=True,
            execute_timeout=self.cfg.execute_timeout,
        )

    async def __aenter__(self):
        return await self.connect()

    async def __aexit__(self, exc_type, *args, **kwargs) -> None:
        await self.close(exc_type)

    def __enter__(self):
        return self.connect_sync()

    def __exit__(self, exc_type, *args, **kwargs) -> None:
        self.close_sync(exc_type)
