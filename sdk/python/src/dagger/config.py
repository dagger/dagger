import os
from pathlib import Path
from typing import TextIO
from urllib.parse import ParseResult as ParsedURL
from urllib.parse import urlparse

from attrs import define, field

from ._engine import ENGINE_IMAGE_REF

DEFAULT_HOST = f"docker-image://{ENGINE_IMAGE_REF}"


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
        is raised. Only used for async transport.
        Passing None results in waiting forever for a response.
    reconnecting:
        If True, create a permanent reconnecting session. Only used for async transport.
    """

    host: ParsedURL = field(
        factory=lambda: os.environ.get("DAGGER_HOST", DEFAULT_HOST),
        converter=urlparse,
    )
    workdir: Path | str = ""
    config_path: Path | str = ""
    log_output: TextIO | None = None
    timeout: int = 10
    execute_timeout: int | float | None = 60 * 5
    reconnecting: bool = True
