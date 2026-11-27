"""The external Python SDK's fixture for the golden tests.

Every SDK fixture has the same functions, so their goldens line up. The check
functions are `dagger check`'s cases; see sdkCheckCases.
"""

from __future__ import annotations

import sys
import time

from opentelemetry import trace
from opentelemetry.trace import Status, StatusCode

import dagger
from dagger import check, function, object_type
from dagger_clients.core import Container, core
from dagger_clients.sdk_dep import sdk_dep

tracer = trace.get_tracer(__name__)


def failing_exec() -> Container:
    return (
        core()
        .container()
        .from_("alpine")
        .with_exec(
            ["sh", "-c", "echo exec stdout; sleep 0.1; echo exec stderr >&2; exit 1"]
        )
    )


def log_both() -> None:
    """Logs a line to stdout, then one to stderr.

    The streams are captured apart, so the pause keeps their order stable in
    the goldens.
    """
    print("python function stdout", flush=True)
    time.sleep(0.1)
    print("python function stderr", file=sys.stderr, flush=True)


@object_type
class ViztestPython:
    @function(cache="session")
    def log_success(self) -> str:
        """Logs to stdout and stderr, and succeeds."""
        log_both()
        return "python function result"

    @function(cache="session")
    def fail_native(self) -> None:
        """Logs to stdout and stderr, then raises an error of its own."""
        log_both()
        raise ValueError("python function failed natively")

    @function(cache="session")
    def fail_exit_code(self) -> None:
        """Logs to stdout and stderr, then exits the process with a custom code."""
        log_both()
        sys.exit(3)

    @function(cache="session")
    async def fail_exec(self) -> str:
        """Runs a command that fails, and lets its error propagate."""
        return await failing_exec().stdout()

    @function(cache="session")
    async def fail_exec_wrapped(self) -> str:
        """Runs a command that fails, and wraps its error."""
        try:
            return await failing_exec().stdout()
        except dagger.ExecError as e:
            raise RuntimeError(f"python function ran a failing command: {e}") from e

    @function(cache="session")
    async def fail_api(self) -> str:
        """Logs a line, then reads a file that doesn't exist, and lets the error propagate."""
        print("python function stdout", flush=True)
        return await core().directory().file("doesnt-exist.txt").contents()

    @function(cache="session")
    async def call_failing_dep(self) -> str:
        """Calls a function of a dependency whose command fails."""
        return await sdk_dep().fail_exec()

    @function(cache="session")
    async def call_failing_dep_logged(self) -> str:
        """Logs a line, then calls a function of a dependency whose command fails."""
        print("python function stdout", flush=True)
        return await sdk_dep().fail_exec()

    @function(cache="session")
    @check
    def check_fail_native(self) -> None:
        """A check that logs to stdout and stderr, then raises an error of its own."""
        log_both()
        raise ValueError("python check failed natively")

    @function(cache="session")
    @check
    async def check_fail_exec(self) -> None:
        """A check that runs a command that fails, and lets its error propagate."""
        await failing_exec().sync()

    @function(cache="session")
    @check
    def check_spans(self) -> None:
        """A check that runs its steps under spans of its own, one of which fails, then raises."""
        failed = []
        for package in ("core", "web"):
            with tracer.start_as_current_span(f"tsc {package}") as span:
                if package == "web":
                    print("web: 2 type errors", file=sys.stderr, flush=True)
                    span.set_status(Status(StatusCode.ERROR, "2 type errors"))
                    failed.append(package)
        raise RuntimeError(f"type errors in {', '.join(failed)}")
