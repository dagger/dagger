"""Commands the generated entrypoint runs in the module's container."""

import argparse
import atexit
import json
import logging
import os
import pathlib
import sys
import threading
from typing import Any

import anyio

from dagger import telemetry
from dagger._exceptions import QueryError
from dagger.client._session import mark_module_runtime
from dagger.mod._exceptions import ModuleError

logger = logging.getLogger(__package__)


def main(argv: list[str] | None = None) -> int:
    """Run one command and return the exit status."""
    mark_module_runtime()
    parser = argparse.ArgumentParser(prog="python -m dagger.mod")
    commands = parser.add_subparsers(required=True)

    entrypoint = commands.add_parser(
        "entrypoint",
        help="render the static entrypoint of the module in the current directory",
    )
    entrypoint.add_argument("--name", required=True, help="module name")
    entrypoint.add_argument(
        "--client-version",
        required=True,
        help="the engine release the module's code is generated for, which the "
        "entrypoint declares when it starts the module's process",
    )
    entrypoint.add_argument("--output", required=True, type=pathlib.Path)
    entrypoint.add_argument(
        "--outside",
        action="append",
        default=[],
        help="a directory outside the module directory that its build reads, "
        "relative to it, once per directory",
    )
    entrypoint.set_defaults(run=_entrypoint)

    describe = commands.add_parser(
        "describe",
        help="write the module's type definitions as JSON for a runtime entrypoint",
    )
    describe.add_argument("--output", required=True, type=pathlib.Path)
    describe.set_defaults(run=_describe)

    call = commands.add_parser(
        "call",
        help="run the call read from standard input and write its JSON result",
    )
    call.add_argument("--output", required=True, type=pathlib.Path)
    call.set_defaults(run=_call)

    args = parser.parse_args(argv)
    try:
        args.run(args)
    except (ModuleError, QueryError) as e:
        logger.error(str(e))  # noqa: TRY400 - the message is the whole story
        return 2
    except Exception:
        logger.exception("Unhandled exception")
        return 1
    return 0


def _entrypoint(args: argparse.Namespace) -> None:
    from dagger.client._descriptor import registering_types
    from dagger.mod._entrypoint import write_entrypoint
    from dagger.mod.cli import load_module

    with registering_types():
        mod = load_module()
    write_entrypoint(
        mod.describe(),
        name=args.name,
        client_version=args.client_version,
        output=args.output,
        outside=args.outside,
    )


def _describe(args: argparse.Namespace) -> None:
    from dagger.client._descriptor import registering_types
    from dagger.mod._describe import describe_json
    from dagger.mod.cli import load_module

    with registering_types():
        mod = load_module()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(describe_json(mod.describe()))


def _call(args: argparse.Namespace) -> None:
    request = json.load(sys.stdin)
    telemetry.initialize()
    try:
        result = anyio.run(_dispatch, request)
    finally:
        telemetry.shutdown()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result))


async def _dispatch(request: dict[str, Any]) -> Any:
    from dagger.mod.cli import load_module

    return await load_module().dispatch(request)


def _exit(code: int) -> None:
    """Exit as a normal exit does, but skip tearing the interpreter down.

    The teardown frees every object the imports made, about 50 ms per call,
    and nothing reads the process after it: the result is written and closed,
    and telemetry is flushed.
    """
    for thread in threading.enumerate():
        if thread is not threading.main_thread() and not thread.daemon:
            thread.join()
    atexit._run_exitfuncs()  # noqa: SLF001 - os._exit skips atexit handlers
    sys.stdout.flush()
    sys.stderr.flush()
    os._exit(code)


if __name__ == "__main__":
    _exit(main())
