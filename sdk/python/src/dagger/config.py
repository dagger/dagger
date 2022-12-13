import os
from pathlib import Path
from typing import TextIO
from urllib.parse import ParseResult as ParsedURL
from urllib.parse import urlparse

from attrs import define, field

from ._version import CLI_VERSION


def host_factory():
    session_url = os.environ.get("DAGGER_SESSION_URL")
    if session_url:
        return session_url
    cli_bin = os.environ.get("_EXPERIMENTAL_DAGGER_CLI_BIN")
    if cli_bin:
        return f"bin://{cli_bin}"
    # TODO: download-bin is not the way we want to model this, it's just an
    # easier stepstone than refactoring all the connector code at once
    return f"download-bin://{CLI_VERSION}"


@define
class Config:
    """Options for connecting to the Dagger engine.

    Parameters
    ----------
    host:
        Address to connect to the engine.
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
    reconnecting:
        If True, create a permanent reconnecting session. Only used for async transport.
    """

    host: ParsedURL = field(
        factory=host_factory,
        converter=urlparse,
    )
    session_token: str | None = os.environ.get("DAGGER_SESSION_TOKEN")
    workdir: Path | str = ""
    config_path: Path | str = ""
    log_output: TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = 60 * 5
    reconnecting: bool = True
