import pytest


@pytest.fixture()
def anyio_backend():
    # FIXME: remove when other backends can be supported
    # (i.e., HTTPX transport since it supports AnyIO)
    return "asyncio"
