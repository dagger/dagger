import httpx
import pytest
from pytest_httpx import HTTPXMock

import dagger
from dagger.exceptions import ExecuteTimeoutError, TransportError

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


@pytest.fixture(scope="module")
def anyio_backend():
    return "asyncio"


@pytest.fixture(scope="module")
async def client():
    async with dagger.Connection() as client:
        yield client


async def test_timeout(client: dagger.Client, httpx_mock: HTTPXMock):
    httpx_mock.add_exception(httpx.ReadTimeout("Request took too long"))

    with pytest.raises(ExecuteTimeoutError, match="execute_timeout"):
        await client.container().id()


async def test_request_error(client: dagger.Client, httpx_mock: HTTPXMock):
    msg = "Server disconnected without sending a response."
    httpx_mock.add_exception(httpx.RemoteProtocolError(msg))

    with pytest.raises(TransportError, match=msg):
        await client.container().id()


@pytest.mark.parametrize(
    "response",
    [
        httpx.Response(status_code=500, text="Oops!"),
        httpx.Response(status_code=200, text="Spam"),
        httpx.Response(status_code=200, json={"content": {}}),
    ],
)
async def test_bad_response(
    response: httpx.Response,
    client: dagger.Client,
    httpx_mock: HTTPXMock,
):
    httpx_mock.add_callback(lambda _: response)

    with pytest.raises(TransportError, match="Unexpected response"):
        await client.container().id()


async def test_good_response_data(client: dagger.Client, httpx_mock: HTTPXMock):
    httpx_mock.add_response(json={"data": {"container": {"id": "spam"}}})

    res = await client.container().id()

    assert res == "spam"


async def test_query_error(client: dagger.Client, httpx_mock: HTTPXMock):
    msg = "invalid reference format"
    error = {
        "message": msg,
        "path": ["container", "from"],
        "locations": [{"line": 3, "column": 5}],
    }
    httpx_mock.add_response(json={"errors": [error]})

    with pytest.raises(dagger.QueryError) as exc_info:
        await client.container().from_("invalid!").id()

    exc = exc_info.value
    assert msg in str(exc)
    assert exc.errors[0].path == ["container", "from"]
    assert exc.errors[0].locations[0].line == 3
    assert exc.errors[0].locations[0].column == 5
