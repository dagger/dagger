import dataclasses
from os import PathLike
from typing import Any, TextIO

from rich.console import Console

from dagger.client._config import ConnectConfig, Timeout

UNSET = object()


@dataclasses.dataclass(slots=True, kw_only=True)
class Config(ConnectConfig):
    """Options for connecting to the Dagger engine.

    Parameters
    ----------
    timeout:
        The maximum time in seconds for establishing a connection to the server,
        or None to disable. Defaults to 10 seconds.
    retry:
        Retry parameters for connecting to the Dagger API server.
    workdir:
        The host workdir loaded into dagger.
    config_path:
        Project config file.
    log_output:
        A TextIO object to send the logs from the engine.
    execute_timeout:
        The maximum time in seconds for the execution of a request before an
        ExecuteTimeoutError is raised. Passing None results in waiting forever for a
        response (default).
    """

    workdir: PathLike[str] | str = ""
    config_path: PathLike[str] | str = ""
    log_output: TextIO | None = None
    execute_timeout: Any = UNSET
    console: Console = dataclasses.field(init=False)

    def __post_init__(self):
        # Backwards compatibility for (expected) use of `timeout` config.
        if self.timeout and not isinstance(self.timeout, Timeout):
            # TODO: deprecation warning: Use
            # self.timeout hasn't worked! (unused)
            timeout = self.timeout

            # used to be int
            try:
                timeout = float(timeout)
            except TypeError as e:
                msg = f"Wrong type for timeout: {type(timeout)}"
                raise TypeError(msg) from e

            self.timeout = Timeout(None, connect=timeout)

        # Backwards compatibility for `execute_timeout` config.
        if self.execute_timeout is not UNSET:
            # TODO: deprecation warning: Use `timeout` instead.
            timeout = self.execute_timeout

            # used to be int | float | None
            if timeout is not None:
                try:
                    timeout = float(timeout)
                except TypeError as e:
                    msg = f"Wrong type for execute_timeout: {type(timeout)}"
                    raise TypeError(msg) from e

            self.timeout = (
                Timeout(timeout, connect=self.timeout.connect)
                if self.timeout
                else Timeout(timeout)
            )

        self.console = Console(
            file=self.log_output,
            stderr=True,
        )
