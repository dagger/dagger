import gc
import warnings
import uuid

import anyio
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
        dagger.TransportError,
        match="Connection to engine has been closed",
    ):
        await client.container().id()


async def test_execute_timeout(alpine_image: str):
    cfg = dagger.Config(execute_timeout=1, retry=None)

    async with dagger.Connection(cfg) as client:
        alpine = client.container().from_(alpine_image)
        with pytest.raises(dagger.TransportError, match="Request timed out"):
            await (
                alpine.with_env_variable("_NO_CACHE", str(uuid.uuid4()))
                .with_exec(["sleep", "2"])
                .stdout()
            )


async def test_connection_close_does_not_warn_about_unclosed_stderr_pipe():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always", ResourceWarning)

        async with dagger.Connection(dagger.Config(retry=None)) as client:
            await client.version()

        for _ in range(3):
            gc.collect()
            await anyio.sleep(0.1)

    leaked = [
        warning
        for warning in caught
        if issubclass(warning.category, ResourceWarning)
        and "unclosed file" in str(warning.message)
    ]
    assert leaked == []
