import json
import logging
import subprocess
import time
from importlib import metadata
from pathlib import Path

import dagger
from dagger.config import ConnectParams
from dagger.context import SyncResourceManager
from dagger.exceptions import SessionError

logger = logging.getLogger(__name__)


OS_ETXTBSY = 26


def get_sdk_version():
    try:
        return metadata.version("dagger-io")
    except metadata.PackageNotFoundError:
        return "n/a"


class CLISession(SyncResourceManager):
    """Start an engine session with a provided CLI path."""

    def __init__(self, cfg: dagger.Config, path: str) -> None:
        super().__init__()
        self.cfg = cfg
        self.path = path
        # no constructor param intentional
        self.is_async = True

    def __enter__(self) -> ConnectParams:
        with self.get_sync_stack() as stack:
            try:
                proc = self._start()
            except (OSError, ValueError, TypeError) as e:
                raise SessionError(e) from e
            stack.push(proc)
            conn = self._get_conn(proc)
        return conn

    def _start(self) -> subprocess.Popen:
        args = [
            self.path,
            "session",
            "--label",
            "dagger.io/sdk.name:python",
            "--label",
            f"dagger.io/sdk.version:{get_sdk_version()}",
            "--label",
            f"dagger.io/sdk.async:{str(self.is_async).lower()}",
        ]
        if self.cfg.workdir:
            args.extend(["--workdir", str(Path(self.cfg.workdir).absolute())])
        if self.cfg.config_path:
            args.extend(["--project", str(Path(self.cfg.config_path).absolute())])

        # Retry starting if "text file busy" error is hit. That error can happen
        # due to a flaw in how Linux works: if any fork of this process happens
        # while the temp binary file is open for writing, a child process can
        # still have it open for writing before it calls exec.
        # See this golang issue (which itself links to bug reports in other
        # langs and the kernel): https://github.com/golang/go/issues/22315
        # Unfortunately, this sort of retry loop is the best workaround. The
        # case is obscure enough that it should not be hit very often at all.
        for _ in range(10):
            try:
                proc = subprocess.Popen(
                    args,  # noqa: S603
                    bufsize=0,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=self.cfg.log_output or subprocess.PIPE,
                    encoding="utf-8",
                )
            except OSError as e:  # noqa: PERF203
                if e.errno != OS_ETXTBSY:
                    raise
                logger.warning("file busy, retrying in 0.1 seconds...")
                time.sleep(0.1)
            else:
                return proc

        msg = "CLI busy"
        raise SessionError(msg)

    def _get_conn(self, proc: subprocess.Popen) -> ConnectParams:
        # TODO: implement engine session timeout (self.cfg.engine_timeout?)
        assert proc.stdout
        conn = proc.stdout.readline()

        # Check if subprocess exited with an error
        if ret := proc.poll():
            out = conn + proc.stdout.read()
            err = proc.stderr.read() if proc.stderr and proc.stderr.readable() else None

            # Reuse error message from CalledProcessError
            exc = subprocess.CalledProcessError(ret, " ".join(proc.args))

            msg = str(exc)
            detail = err or out
            if detail and detail.strip():
                # `msg` ends in a period, just append
                msg = f"{msg} {detail.strip()}"

            raise SessionError(msg)

        if not conn:
            msg = "No connection params"
            raise SessionError(msg)

        try:
            return ConnectParams(**json.loads(conn))
        except (ValueError, TypeError) as e:
            msg = f"Invalid connection params: {conn}"
            raise SessionError(msg) from e
