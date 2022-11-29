import logging
import platform
from typing import TypeVar

import httpx
from attrs import Factory, define
from gql.client import Client as GraphQLClient
from gql.transport import AsyncTransport, Transport

from dagger import Client, Config, SyncClient

from .transport.httpx import HTTPXAsyncTransport, HTTPXTransport
from .transport.namedpipe import NamedPipeAsyncTransport, NamedPipeSyncTransport

logger = logging.getLogger(__name__)


_T = TypeVar("_T")


@define
class Connector:
    """Facilitates connecting to the engine."""

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
        httpx_cls = httpx.AsyncHTTPTransport
        if platform.uname().system.lower() == "windows":
            httpx_cls = NamedPipeAsyncTransport
        return self._make_transport(HTTPXAsyncTransport, httpx_cls)

    def make_sync_transport(self) -> Transport:
        httpx_cls = httpx.HTTPTransport
        if platform.uname().system.lower() == "windows":
            httpx_cls = NamedPipeSyncTransport
        return self._make_transport(HTTPXTransport, httpx_cls)

    def _make_transport(
        self,
        gql_cls: type[_T],
        httpx_cls: type[
            httpx.AsyncHTTPTransport
            | httpx.HTTPTransport
            | NamedPipeAsyncTransport
            | NamedPipeSyncTransport
        ],
    ) -> _T:
        if self.cfg.host.scheme not in ("unix",):
            raise ValueError(f"Unsupported scheme {self.cfg.host.scheme}")
        path = self.cfg.host.netloc + self.cfg.host.path
        transport = httpx_cls(uds=path)
        return gql_cls(
            "http://dagger/query",
            transport=transport,
            timeout=self.cfg.execute_timeout,
        )

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
