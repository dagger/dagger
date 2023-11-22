import uuid

import pytest

import dagger

pytestmark = [
    pytest.mark.anyio,
    pytest.mark.slow,
]


async def test_connection_closed_error():
    async with dagger.Connection(dagger.Config(retry=None)) as client:
        ...
    with pytest.raises(
        dagger.TransportError, match="Connection to engine has been closed"
    ):
        await client.container().id()


async def test_execute_timeout():
    cfg = dagger.Config(execute_timeout=0.5, retry=None)

    async with dagger.Connection(cfg) as client:
        alpine = client.container().from_("alpine:3.16.2")
        with pytest.raises(dagger.ExecuteTimeoutError):
            await (
                alpine.with_env_variable("_NO_CACHE", str(uuid.uuid4()))
                .with_exec(["sleep", "2"])
                .stdout()
            )
