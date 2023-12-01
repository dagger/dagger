import httpx
import pytest
from pytest_httpx import HTTPXMock

import dagger

pytestmark = [
    pytest.mark.anyio,
]


@pytest.fixture(scope="module")
def anyio_backend():
    return "asyncio"


@pytest.fixture(scope="module")
async def client():
    async with dagger.Connection(dagger.Config(retry=None)) as client:
        yield client


@pytest.mark.slow()
async def test_timeout(client: dagger.Client, httpx_mock: HTTPXMock):
    httpx_mock.add_exception(httpx.ReadTimeout("Request took too long"))
    msg = "Try setting a higher timeout value"

    with pytest.raises(dagger.ExecuteTimeoutError, match=msg):
        await client.container()


async def test_request_error(client: dagger.Client, httpx_mock: HTTPXMock):
    msg = "Server disconnected without sending a response."
    httpx_mock.add_exception(httpx.RemoteProtocolError(msg))

    with pytest.raises(dagger.TransportError, match=msg):
        await client.container()


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

    with pytest.raises(dagger.TransportError, match="Unexpected response"):
        await client.container()


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
        await client.container().from_("invalid!")

    exc = exc_info.value
    assert msg in str(exc)
    assert exc.errors[0].path == ["container", "from"]
    assert exc.errors[0].locations is not None
    assert exc.errors[0].locations[0].line == 3
    assert exc.errors[0].locations[0].column == 5


async def test_no_path_query_error(client: dagger.Client, httpx_mock: HTTPXMock):
    msg = "invalid request"
    error = {
        "message": msg,
    }
    httpx_mock.add_response(json={"errors": [error]})

    with pytest.raises(dagger.QueryError) as exc_info:
        await client.container().from_("invalid!")

    exc = exc_info.value
    assert msg in str(exc)
    assert exc.errors[0].path is None
    assert exc.errors[0].locations is None


@pytest.mark.slow()
async def test_exec_error(client: dagger.Client, httpx_mock: HTTPXMock):
    error = {
        "message": "command not found",
        "path": ["container", "from", "withExec"],
        "locations": [{"line": 3, "column": 5}],
        "extensions": {
            "_type": "EXEC_ERROR",
            "cmd": ["sh", "-c", "spam"],
            "exitCode": 127,
            "stdout": "",
            "stderr": "/bin/sh: spam: not found",
        },
    }
    httpx_mock.add_response(json={"errors": [error]})
    ctr = client.container().from_("alpine").with_exec(["sh", "-c", "spam"])

    with pytest.raises(dagger.ExecError) as exc_info:
        await ctr

    exc = exc_info.value
    assert issubclass(exc.__class__, dagger.QueryError)

    assert exc.message == "command not found"
    assert exc.command == ["sh", "-c", "spam"]
    assert exc.exit_code == 127
    assert exc.stderr == "/bin/sh: spam: not found"
    assert exc.stdout == ""

    assert "command not found" in str(exc)
    assert "spam: not found" in str(exc)
