import typing
from dataclasses import dataclass, field
from os import PathLike

from rich.console import Console


@dataclass(slots=True, kw_only=True)
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
    console: Console = field(init=False)

    def __post_init__(self):
        self.console = Console(
            file=self.log_output,
            stderr=True,
        )
