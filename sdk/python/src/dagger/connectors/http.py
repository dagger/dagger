import logging
from typing import TypeVar

from attrs import define
from gql.transport import AsyncTransport, Transport

from dagger.transport.httpx import HTTPXAsyncTransport, HTTPXTransport

from .base import Connector, register_connector

logger = logging.getLogger(__name__)


_T = TypeVar("_T")


@register_connector("http")
@define
class HTTPConnector(Connector):
    """Connect to dagger engine via HTTP"""

    @property
    def query_url(self) -> str:
        return f"{self.cfg.host.geturl()}/query"

    def _make_transport(self, cls: type[_T]) -> _T:
        return cls(self.query_url, timeout=self.cfg.execute_timeout)

    def make_transport(self) -> AsyncTransport:
        return self._make_transport(HTTPXAsyncTransport)

    def make_sync_transport(self) -> Transport:
        return self._make_transport(HTTPXTransport)
