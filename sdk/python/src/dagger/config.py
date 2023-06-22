import typing
from dataclasses import dataclass, field
from os import PathLike

import httpx


@dataclass
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

    workdir: PathLike[str] | str = ""
    config_path: PathLike[str] | str = ""
    log_output: typing.TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = None


@dataclass(kw_only=True)
class ConnectParams:
    """Options for making a session connection. For internal use only."""

    port: int
    session_token: str
    url: httpx.URL = field(init=False)

    def __post_init__(self):
        self.port = int(self.port)
        if self.port < 1:
            msg = f"Invalid port value: {self.port}"
            raise ValueError(msg)
        self.url = httpx.URL(f"http://127.0.0.1:{self.port}/query")
