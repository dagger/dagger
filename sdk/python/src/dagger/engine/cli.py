import contextlib
import io
import logging
import subprocess
import threading
import time
from json.decoder import JSONDecodeError
from pathlib import Path
from typing import TextIO, cast

import cattrs
from cattrs.preconf.json import JsonConverter

import dagger
from dagger.config import ConnectParams
from dagger.context import SyncResourceManager
from dagger.exceptions import SessionError

logger = logging.getLogger(__name__)


OS_ETXTBSY = 26
"""Text file busy error."""


class StreamReader(threading.Thread, contextlib.AbstractContextManager):
    """Read from subprocess pipe and write to in-memory buffer.

    Reading from the pipe in a thread is the simplest non-blocking
    solution that's also platform independent.

    Optionally can provide an open file to also write to. Common usage
    would be `sys.stderr` to get subprocess's stderr in the terminal.
    It's the responsibility of the caller to open and close this file.
    """

    def __init__(self, reader: TextIO, file: TextIO | None):
        super().__init__()
        self.deamon = True
        self.reader = reader
        self.file = file
        self.buffer = io.StringIO()
        self.stop = threading.Event()

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, *exc_info):
        self.close()

    def run(self):
        """Read from stream in a thread."""
        # GIL is released during I/O so this shouldn't block.
        lines = iter(self.reader.readline, "")
        while not self.stop.is_set() and not self.reader.closed:
            # Lines are always consumed until closed to prevent buffer from
            # getting full. Otherwise it'll block the child process.
            # Pipes need to be read.
            try:
                # Not iterating in a for loop to be easier to control when
                # to stop. Otherwise it blocks on readline at the end of
                # dagger.Connection before allowing the main thread to close
                # that process. This way we reevaluate after every read.
                line = next(lines)
            except StopIteration:
                break
            if self.file:
                self.file.write(line)
            if not self.buffer.closed:
                self.buffer.write(line)

    def read(self) -> str | None:
        """Read everything in buffer."""
        return self.buffer.getvalue()

    def discard(self) -> None:
        """Stop writing to buffer."""
        # Close buffer to not waste memory if startup was successful. At this
        # point we don't care about an error during connection anymore.
        self.buffer.close()

    def close(self) -> None:
        self.stop.set()
        if self.file:
            self.file.flush()
        if not self.buffer.closed:
            self.buffer.close()


class CLISession(SyncResourceManager):
    """Start an engine session with a provided CLI path."""

    def __init__(self, cfg: dagger.Config, path: str) -> None:
        super().__init__()
        self.cfg = cfg
        self.path = path
        self.converter = JsonConverter()

        if self.cfg.log_output and self.cfg.log_output.closed:
            # This will raise an exception later when trying to write to
            # the closed file, but let it. It's probably less surprising
            # to fail then to proceed silently.
            logger.warning("File in log_output is closed.")

    def __enter__(self) -> ConnectParams:
        with self.get_sync_stack() as stack:
            try:
                proc = self._start()
            except (OSError, ValueError, TypeError) as e:
                raise SessionError(e) from e
            stack.push(proc)
            # Write stderr to log_output but also to a temporary in-memory
            # buffer, until connection is established without errors.
            # Otherwise we can't capture the error if using log_output
            # instead of a PIPE, to include it in the exception message.
            reader = StreamReader(cast(TextIO, proc.stderr), self.cfg.log_output)
            stderr = stack.enter_context(reader)
            conn = self._get_conn(proc, stderr)
            # Startup ok, stop writing stderr to buffer.
            stderr.discard()

        return conn

    def _start(self) -> subprocess.Popen:
        args = [self.path, "session"]
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
                    args,
                    bufsize=0,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    encoding="utf-8",
                )
            except OSError as e:
                if e.errno != OS_ETXTBSY:
                    raise
                logger.warning("file busy, retrying in 0.1 seconds...")
                time.sleep(0.1)
            else:
                return proc

        msg = "CLI busy"
        raise SessionError(msg)

    def _get_conn(self, proc: subprocess.Popen, stderr: StreamReader) -> ConnectParams:
        # FIXME: implement engine session timeout (self.cfg.engine_timeout?)
        stdout = cast(TextIO, proc.stdout)
        conn = stdout.readline()

        # Check if subprocess exited with an error.
        if ret := proc.poll():
            args = cast(list, proc.args)
            out = conn + stdout.read()

            # Make sure the thread has finished reading.
            logger.debug("Joining with stderr reader thread")
            stderr.join()
            err = stderr.read()

            # Reuse error message from CalledProcessError.
            exc = subprocess.CalledProcessError(ret, " ".join(args))

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
            return self.converter.loads(conn, ConnectParams)
        except (JSONDecodeError, cattrs.BaseValidationError) as e:
            msg = f"Invalid connection params: {conn}"
            raise SessionError(msg) from e
