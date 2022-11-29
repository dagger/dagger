import logging
from typing import Optional

import httpcore
import httpx
from httpcore.backends.base import AsyncNetworkStream, NetworkBackend, NetworkStream

log = logging.getLogger(__name__)


class NamedPipeSyncTransport(httpx.BaseTransport):
    def __init__(self, uds: str):
        self._pipe_name = uds

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        backend = NamedPipeNetworkBackend(self._pipe_name)
        origin = httpcore.Origin(
            scheme=request.url.raw_scheme, host=request.url.raw_host, port=80
        )
        conn = httpcore.HTTPConnection(
            origin=origin, uds=self._pipe_name, network_backend=backend
        )
        req = httpcore.Request(
            method=request.method,
            url=httpcore.URL(
                scheme=request.url.raw_scheme,
                host=request.url.raw_host,
                port=request.url.port,
                target=request.url.raw_path,
            ),
            headers=request.headers.raw,
            content=request.stream,
            extensions=request.extensions,
        )
        resp = conn.handle_request(req)
        return httpx.Response(
            status_code=resp.status,
            headers=resp.headers,
            content=resp.stream,
            extensions=resp.extensions,
        )


class NamedPipeAsyncTransport(httpx.AsyncBaseTransport):
    def __init__(self, uds: str):
        self._pipe_name = uds

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        backend = NamedPipeAsyncNetworkBackend(self._pipe_name)
        origin = httpcore.Origin(
            scheme=request.url.raw_scheme, host=request.url.raw_host, port=80
        )
        conn = httpcore.AsyncHTTPConnection(
            origin=origin, uds=self._pipe_name, network_backend=backend
        )
        req = httpcore.Request(
            method=request.method,
            url=httpcore.URL(
                scheme=request.url.raw_scheme,
                host=request.url.raw_host,
                port=request.url.port,
                target=request.url.raw_path,
            ),
            headers=request.headers.raw,
            content=request.stream,
            extensions=request.extensions,
        )
        resp = await conn.handle_async_request(req)
        return httpx.Response(
            status_code=resp.status,
            headers=resp.headers,
            content=resp.stream,
            extensions=resp.extensions,
        )


class NamedPipeNetworkBackend(NetworkBackend):
    def __init__(self, pipe_name):
        self._pipe_name = pipe_name

    def connect_unix_socket(
        self, path: str, timeout: Optional[float] = None
    ) -> NetworkStream:
        return NamedPipeNetworkStream(self._pipe_name)


class NamedPipeAsyncNetworkBackend(NetworkBackend):
    def __init__(self, pipe_name):
        self._pipe_name = pipe_name

    async def connect_unix_socket(
        self, path: str, timeout: Optional[float] = None
    ) -> NetworkStream:
        return NamedPipeAsyncNetworkStream(self._pipe_name)


class NamedPipeNetworkStream(NetworkStream):
    def __init__(self, pipe_name):
        import win32file

        self._pipe_name = pipe_name
        self._handle = win32file.CreateFile(
            self._pipe_name,
            win32file.GENERIC_READ | win32file.GENERIC_WRITE,
            0,
            None,
            win32file.OPEN_EXISTING,
            0,
            None,
        )

    def read(self, max_bytes: int, timeout: Optional[float] = None) -> bytes:
        import win32file

        if self._handle is None:
            raise httpcore.ClosedResourceError
        _, result = win32file.ReadFile(self._handle, max_bytes)
        assert isinstance(result, bytes)
        return result

    def write(self, buffer: bytes, timeout: Optional[float] = None) -> None:
        import win32file

        if self._handle is None:
            raise httpcore.ClosedResourceError
        win32file.WriteFile(self._handle, buffer)

    def close(self) -> None:
        import win32file

        if self._handle is None:
            return
        win32file.CloseHandle(self._handle)
        self._handle = None


class NamedPipeAsyncNetworkStream(AsyncNetworkStream):
    def __init__(self, pipe_name):
        import win32file

        self._pipe_name = pipe_name
        self._handle = win32file.CreateFile(
            self._pipe_name,
            win32file.GENERIC_READ | win32file.GENERIC_WRITE,
            0,
            None,
            win32file.OPEN_EXISTING,
            0,
            None,
        )

    async def read(self, max_bytes: int, timeout: Optional[float] = None) -> bytes:
        import win32file

        if self._handle is None:
            raise httpcore.ClosedResourceError
        _, result = win32file.ReadFile(self._handle, max_bytes)
        assert isinstance(result, bytes)
        return result

    async def write(self, buffer: bytes, timeout: Optional[float] = None) -> None:
        import win32file

        if self._handle is None:
            raise httpcore.ClosedResourceError
        win32file.WriteFile(self._handle, buffer)

    async def close(self) -> None:
        import win32file

        if self._handle is None:
            return
        win32file.CloseHandle(self._handle)
        self._handle = None
