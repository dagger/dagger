import contextlib
import logging
from typing import TypeAlias, TypeVar

import httpx
from gql.client import AsyncClientSession, SyncClientSession
from gql.client import Client as GraphQLClient
from gql.transport import AsyncTransport, Transport
from gql.transport.exceptions import (
    TransportProtocolError,
    TransportQueryError,
    TransportServerError,
)

from dagger.exceptions import ClientConnectionError

from .config import Config, ConnectParams
from .context import ResourceManager, SyncResourceManager
from .transport.httpx import HTTPXAsyncTransport, HTTPXTransport

logger = logging.getLogger(__name__)


_T = TypeVar("_T")


ClientSession: TypeAlias = AsyncClientSession | SyncClientSession


class Session(ResourceManager, SyncResourceManager):
    """Establish a GraphQL client connection to the engine."""

    def __init__(self, conn: ConnectParams, cfg: Config):
        super().__init__()
        self.conn = conn
        self.cfg = cfg

    def make_transport(self) -> AsyncTransport:
        return self._make_transport(HTTPXAsyncTransport)

    def make_sync_transport(self) -> Transport:
        return self._make_transport(HTTPXTransport)

    def _make_transport(self, cls: type[_T]) -> _T:
        return cls(
            self.conn.url,
            timeout=self.cfg.execute_timeout,
            auth=(self.conn.session_token, ""),
        )

    def make_graphql_client(
        self,
        transport: AsyncTransport | Transport,
    ) -> GraphQLClient:
        return GraphQLClient(
            transport=transport,
            fetch_schema_from_transport=True,
            # Don't timeout with the event loop. This setting is only for AsyncTransport
            # and uses `asyncio.wait_for` which is not compatible with other event loops
            # (e.g., Trio). Catching the TimeoutError would also be more complicated,
            # unless *gql* adopts AnyIO in the future, but still only on *async*.
            # Since we're using *httpx* as the transport for both *async* and *sync*, we
            # can use that project's Timeout instead for both environments.
            execute_timeout=None,
        )

    async def __aenter__(self) -> AsyncClientSession:
        transport = self.make_transport()
        client = self.make_graphql_client(transport)

        async with self.get_stack() as stack:
            # TODO: handle cancellation, retries and timeout (self.cfg.timeout)
            with self._handle_connection():
                session = await stack.enter_async_context(client)

        return session

    def __enter__(self) -> SyncClientSession:
        transport = self.make_sync_transport()
        client = self.make_graphql_client(transport)

        with self.get_sync_stack() as stack, self._handle_connection():
            # TODO: handle cancellation, retries and timeout (self.cfg.timeout)
            session = stack.enter_context(client)

        return session

    @contextlib.contextmanager
    def _handle_connection(self):
        # Reduces duplication when handling errors, between sync and async.
        try:
            yield
        except httpx.RequestError as e:
            msg = f"Could not make request: {e}"
            raise ClientConnectionError(msg) from e
        except (TransportProtocolError, TransportServerError) as e:
            msg = f"Got unexpected response from engine: {e}"
            raise ClientConnectionError(msg) from e
        except TransportQueryError as e:
            # Only query during connection is the introspection query
            # for building the schema.
            msg = str(e)
            # Extract only the error message.
            if e.errors and "message" in e.errors[0]:
                msg = e.errors[0]["message"].strip()
            msg = f"Failed to build schema from introspection query: {msg}"
            raise ClientConnectionError(msg) from e
