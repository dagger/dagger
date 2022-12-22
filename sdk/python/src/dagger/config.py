import pathlib
import typing

import attrs
import httpx


@attrs.define
class Config:
    """Options for connecting to the Dagger engine.

    Parameters
    ----------
    workdir:
        The host workdir loaded into dagger.
    config_path:
        Project config file.
    log_output:
        A TextIO object to send the logs from the engine.
    timeout:
        The maximum time in seconds for establishing a connection to the server.
    execute_timeout:
        The maximum time in seconds for the execution of a request before a TimeoutError
        is raised. Passing None results in waiting forever for a response.
    """

    workdir: pathlib.Path | str = ""
    config_path: pathlib.Path | str = ""
    log_output: typing.TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = 60 * 5


def _host_converter(value: str) -> httpx.URL:
    # Soon host will be replaced by just a port which is much simpler to validate.
    # Just do some basic checks in the meantime, not meant to be exhaustive.
    if "://" not in value:
        value = f"http://{value}"
    try:
        url = httpx.URL(value)
    except httpx.InvalidURL as e:
        raise ValueError(f"Invalid host: {value}") from e
    if url.scheme != "http":
        raise ValueError(f"Unsupported scheme in host: {value}. Expected http.")
    if not url.port:
        raise ValueError(f"No port found in host: {value}")
    return url


@attrs.define
class ConnectParams:
    """Options for making a session connection. For internal use only."""

    host: httpx.URL = attrs.field(converter=_host_converter)
    session_token: str
