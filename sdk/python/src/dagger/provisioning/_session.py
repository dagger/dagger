import collections
import contextlib
import dataclasses
import json
import logging
import subprocess
import threading
import time
from collections.abc import Iterable
from importlib import metadata
from pathlib import Path
from typing import TextIO, cast

from typing_extensions import Self

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
            session = stack.enter_context(run(cfg, path))
            params = get_connect_params(session)
            stack.push(Pclose(session.proc))
            yield params
    except (OSError, ValueError, TypeError) as e:
        raise SessionError(e) from e


def _has_fileno(stream: TextIO) -> bool:
    """Check if a stream has a valid file descriptor."""
    try:
        stream.fileno()
    except (AttributeError, OSError):
        return False
    return True


def _forward_stderr(source: TextIO, dest: TextIO) -> None:
    """Forward lines from source to dest until EOF."""
    try:
        with contextlib.suppress(ValueError):
            dest.writelines(source)
    finally:
        with contextlib.suppress(OSError, ValueError):
            source.close()


class _TailBuffer:
    """Append-only line buffer that keeps only the most recent lines.

    Used to drain the engine's stderr pipe when the user hasn't configured
    log_output. Without a drain, the pipe buffer fills (~64 KB on Linux) and
    the engine blocks writing logs, which deadlocks session shutdown.
    """

    def __init__(self, maxlines: int = 200) -> None:
        self._lines: collections.deque[str] = collections.deque(maxlen=maxlines)

    def writelines(self, lines: Iterable[str]) -> None:
        self._lines.extend(lines)

    def write(self, s: str) -> None:
        self._lines.append(s)

    def getvalue(self) -> str:
        return "".join(self._lines)


@dataclasses.dataclass(slots=True)
class _StartedSession(contextlib.AbstractContextManager):
    """Bundle the dagger session subprocess with its background drain state.

    Avoids monkey-patching attributes onto :class:`subprocess.Popen` and gives
    callers (`get_connect_params`, error reporting) a typed way to reach the
    captured stderr.
    """

    proc: subprocess.Popen[str]
    stderr_tail: _TailBuffer | None = None
    stderr_thread: threading.Thread | None = None

    def __enter__(self) -> Self:
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        self.proc.__exit__(exc_type, exc_value, traceback)


def _build_session_args(cfg: Config, path: str) -> list[str]:
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
    if cfg.load_workspace_modules:
        args.append("--load-workspace-modules")
    return args


def _resolve_stderr(
    log_output: TextIO | None,
) -> tuple[int | TextIO, TextIO | None]:
    """Decide stderr destination and what (if anything) drains it.

    If log_output has a real file descriptor, we can let the child write to
    it directly. Otherwise (StringIO, no log_output at all) we use a PIPE
    and drain it from a background thread: without a drain, the ~64 KB pipe
    buffer fills up and the engine blocks writing logs, which deadlocks
    session shutdown.
    """
    if log_output is not None and _has_fileno(log_output):
        return log_output, None
    drain_dest: TextIO = log_output if log_output is not None else _TailBuffer()
    return subprocess.PIPE, drain_dest


def _spawn_with_etxtbsy_retry(
    args: list[str],
    stderr_target: int | TextIO,
) -> subprocess.Popen[str]:
    """Start the session subprocess, retrying on ETXTBSY.

    The "text file busy" error can happen due to a flaw in how Linux works:
    if any fork of this process happens while the temp binary file is open
    for writing, a child process can still have it open for writing before
    it calls exec. See https://github.com/golang/go/issues/22315 for context.
    """
    for _ in range(10):
        try:
            return subprocess.Popen(  # noqa: S603
                args,
                bufsize=0,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=stderr_target,
                encoding="utf-8",
            )
        except OSError as e:  # noqa: PERF203
            if e.errno != OS_ETXTBSY:
                raise
            logger.warning("file busy, retrying in 0.1 seconds...")
            time.sleep(0.1)
    msg = "CLI busy"
    raise SessionError(msg)


def run(cfg: Config, path: str) -> _StartedSession:
    args = _build_session_args(cfg, path)
    stderr_target, drain_dest = _resolve_stderr(cfg.log_output)
    proc = _spawn_with_etxtbsy_retry(args, stderr_target)
    session = _StartedSession(proc=proc)
    if drain_dest is not None and proc.stderr:
        thread = threading.Thread(
            target=_forward_stderr,
            args=(proc.stderr, drain_dest),
            daemon=True,
        )
        thread.start()
        session.stderr_thread = thread
        if isinstance(drain_dest, _TailBuffer):
            session.stderr_tail = drain_dest
        # Forwarding thread now owns the pipe; clear proc.stderr so callers
        # don't race with it over the same fd.
        proc.stderr = None
    return session


def _read_session_stderr(session: _StartedSession) -> str | None:
    """Read whatever stderr we have for a session, if any.

    When stderr was piped without forwarding, we may read it directly. When
    it was drained by the background thread into a tail buffer, we wait
    briefly for the thread to finish flushing, then read from the buffer.
    """
    proc = session.proc
    if proc.stderr and proc.stderr.readable():
        return proc.stderr.read()
    if session.stderr_tail is None:
        return None
    if session.stderr_thread is not None:
        session.stderr_thread.join(timeout=1.0)
    return session.stderr_tail.getvalue()


def get_connect_params(session: _StartedSession) -> ConnectParams:
    # TODO: implement engine session timeout (self.cfg.engine_timeout?)
    proc = session.proc
    assert proc.stdout
    conn = proc.stdout.readline()

    # Check if subprocess exited with an error
    if proc.poll():
        stdout = conn + proc.stdout.read()
        stderr = _read_session_stderr(session)
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
