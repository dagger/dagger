import httpx
import pytest
from pytest_httpx import HTTPXMock

import dagger

pytestmark = [
    pytest.mark.anyio,
]


@pytest.fixture(autouse=True)
def _fake_connection(monkeypatch: pytest.MonkeyPatch):
    # Fake connection params to skip provisioning.
    # Requests will be mocked.
    monkeypatch.setenv("DAGGER_SESSION_PORT", "3000")
    monkeypatch.setenv("DAGGER_SESSION_TOKEN", "spam")


async def test_request_error(httpx_mock: HTTPXMock):
    msg = "All connection attempts failed"
    httpx_mock.add_exception(httpx.ConnectError(msg))

    with pytest.raises(dagger.ClientConnectionError, match=msg):
        async with dagger.Connection():
            ...


@pytest.mark.parametrize(
    "response",
    [
        httpx.Response(status_code=200, text=""),
        httpx.Response(status_code=500, json="Oops!"),
    ],
)
async def test_bad_response(response: httpx.Response, httpx_mock: HTTPXMock):
    httpx_mock.add_callback(lambda _: response)

    with pytest.raises(dagger.ClientConnectionError, match="unexpected response"):
        async with dagger.Connection():
            ...


async def test_introspection_error(httpx_mock: HTTPXMock):
    httpx_mock.add_response(
        json={
            "errors": [
                {
                    "message": "Lorem ipsum",
                    "path": ["x"],
                    "locations": {"line": 10, "column": 10},
                },
            ],
        }
    )
    with pytest.raises(dagger.ClientConnectionError, match="Failed to build schema"):
        async with dagger.Connection():
            ...
