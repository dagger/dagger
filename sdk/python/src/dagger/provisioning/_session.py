import contextlib
import dataclasses
import io
import json
import logging
import os
import subprocess
import threading
import time
from importlib import metadata
from pathlib import Path
from typing import TextIO, cast

from dagger._managers import SyncResource
from dagger.client._session import ConnectParams

from ._config import Config
from ._exceptions import SessionError

logger = logging.getLogger(__name__)


OS_ETXTBSY = 26


def get_sdk_version():
    try:
        return metadata.version("dagger-io")
    except metadata.PackageNotFoundError:
        return "n/a"


def start_cli_session(cfg: Config, path: str):
    # TODO: Convert calling session subprocess to async.
    return SyncResource(start_cli_session_sync(cfg, path))


@dataclasses.dataclass(slots=True)
class Pclose(contextlib.AbstractContextManager):
    """Close process by closing stdin and waiting for it to exit."""

    proc: subprocess.Popen[str]

    # Set a long timeout to give time for any cache exports to pack layers up
    # which currently has to happen synchronously with the session.
    timeout: int = 300

    def __exit__(self, exc_type, exc_value, traceback):
        # Kill the child process by closing stdin, not via SIGKILL, so it has
        # a chance to drain logs.
        try:
            if self.proc.stdin:
                self.proc.stdin.close()
        except AttributeError:
            # FakeProcess doesn't have a stdin attribute (tests)
            self.proc.terminate()

        try:
            self._wait()
        except Exception:  # Including KeyboardInterrupt, wait handled that.
            self.proc.kill()
            # We don't call proc.wait() again as proc.__exit__ does that for us.
            raise

    def _wait(self):
        # avoids raise-within-try (TRY301)
        if self.proc.wait(self.timeout):
            # non-zero exit code
            msg = make_process_error_msg(self.proc, None, None)
            raise SessionError(msg)


@contextlib.contextmanager
def start_cli_session_sync(cfg: Config, path: str):
    """Start an engine session with a provided CLI path."""
    logger.debug("Starting session using %s", path)
    try:
        with contextlib.ExitStack() as stack:
            proc = stack.enter_context(run(cfg, path))
            params = get_connect_params(proc)
            stack.push(Pclose(proc))
            yield params
    except (OSError, ValueError, TypeError) as e:
        raise SessionError(e) from e


def _has_fileno(stream: TextIO) -> bool:
    """Check if a stream has a real file descriptor."""
    try:
        stream.fileno()
    except (io.UnsupportedOperation, OSError, AttributeError):
        return False
    return True


def _forward_stream(read_fd: int, output: TextIO) -> None:
    """Forward data from a pipe file descriptor to a TextIO stream."""
    with os.fdopen(read_fd, "r") as reader:
        for line in reader:
            try:
                output.write(line)
            except (ValueError, OSError):
                break


def run(cfg: Config, path: str) -> subprocess.Popen[str]:
    args = [
        path,
        "session",
        "--label",
        "dagger.io/sdk.name:python",
        "--label",
        f"dagger.io/sdk.version:{get_sdk_version()}",
    ]
    if cfg.workdir:
        args.extend(["--workdir", str(Path(cfg.workdir).absolute())])
    if cfg.config_path:
        args.extend(["--project", str(Path(cfg.config_path).absolute())])

    # If log_output doesn't have a real file descriptor (e.g. StringIO),
    # use a pipe and forward output in a background thread.
    stderr = cfg.log_output or subprocess.PIPE
    pipe_write_fd = None
    if cfg.log_output is not None and not _has_fileno(cfg.log_output):
        read_fd, pipe_write_fd = os.pipe()
        stderr = pipe_write_fd
        threading.Thread(
            target=_forward_stream,
            args=(read_fd, cfg.log_output),
            daemon=True,
        ).start()

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
            proc = subprocess.Popen(  # noqa: S603
                args,
                bufsize=0,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=stderr,
                encoding="utf-8",
            )
        except OSError as e:  # noqa: PERF203
            if e.errno != OS_ETXTBSY:
                raise
            logger.warning("file busy, retrying in 0.1 seconds...")
            time.sleep(0.1)
        else:
            # Close parent's copy of the write end so the forwarding thread
            # gets EOF when the subprocess exits.
            if pipe_write_fd is not None:
                os.close(pipe_write_fd)
            return proc

    if pipe_write_fd is not None:
        os.close(pipe_write_fd)
    msg = "CLI busy"
    raise SessionError(msg)


def get_connect_params(proc: subprocess.Popen[str]) -> ConnectParams:
    # TODO: implement engine session timeout (self.cfg.engine_timeout?)
    assert proc.stdout
    conn = proc.stdout.readline()

    # Check if subprocess exited with an error
    if proc.poll():
        stdout = conn + proc.stdout.read()
        stderr = proc.stderr.read() if proc.stderr and proc.stderr.readable() else None
        msg = make_process_error_msg(proc, stdout, stderr)
        raise SessionError(msg)

    if not conn:
        msg = "No connection params"
        raise SessionError(msg)

    try:
        return ConnectParams(**json.loads(conn))
    except (ValueError, TypeError) as e:
        msg = f"Invalid connection params: {conn}"
        raise SessionError(msg) from e


def make_process_error_msg(
    proc: subprocess.Popen[str],
    stdout: str | None,
    stderr: str | None,
) -> str:
    args = cast(list[str], proc.args)

    # Reuse error message from CalledProcessError
    exc = subprocess.CalledProcessError(proc.returncode, " ".join(args))

    msg = str(exc)
    detail = stderr or stdout
    if detail and detail.strip():
        # `msg` ends in a period, just append
        msg = f"{msg} {detail.strip()}"

    return msg
