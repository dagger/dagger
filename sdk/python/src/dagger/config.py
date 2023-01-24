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
        The maximum time in seconds for the execution of a request before an
        ExecuteTimeoutError is raised. Passing None results in waiting forever for a
        response (default).
    """

    workdir: pathlib.Path | str = ""
    config_path: pathlib.Path | str = ""
    log_output: typing.TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = None


@attrs.define
class ConnectParams:
    """Options for making a session connection. For internal use only."""

    port: int = attrs.field(converter=int)
    session_token: str

    @port.validator
    def _check_port(self, _, value):
        if value < 1:
            msg = f"Invalid port value: {value}"
            raise ValueError(msg)

    @property
    def url(self):
        return httpx.URL(f"http://127.0.0.1:{self.port}/query")
