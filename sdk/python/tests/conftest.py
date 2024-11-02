import pytest


@pytest.fixture(scope="session", autouse=True)
def setup_telemetry():
    import dagger.telemetry

    dagger.telemetry.initialize()


@pytest.fixture(scope="session")
def anyio_backend():
    # TODO: remove when other backends can be supported
    # (i.e., HTTPX transport since it supports AnyIO)
    return "asyncio"


@pytest.fixture
def alpine_version():
    return "3.20.2"


@pytest.fixture
def alpine_image(alpine_version):
    return f"alpine:{alpine_version}"
