import pytest


@pytest.fixture()
def anyio_backend():
    # TODO: remove when other backends can be supported
    # (i.e., HTTPX transport since it supports AnyIO)
    return "asyncio"
