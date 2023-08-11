import logging
import sys
from dataclasses import dataclass
from typing import Annotated, Any, TypeVar

import anyio
import cattrs
import rich
import typer
from cattrs.preconf.json import JsonConverter
from rich.console import Console

import dagger
from dagger.log import configure_logging

from ._checks import CheckResolver
from ._commands import CommandResolver
from ._converter import make_converter
from ._exceptions import FatalError
from ._functions import FunctionResolver
from ._resolver import Resolver
from ._shells import ShellResolver

errors = Console(stderr=True)
logger = logging.getLogger(__name__)

inputs_path = anyio.Path("/inputs/dagger.json")
outputs_path = anyio.Path("/outputs/dagger.json")
envid_path = anyio.Path("/outputs/envid")

T = TypeVar("T")


@dataclass(slots=True, kw_only=True)
class Inputs:
    resolver: str
    args: dict[str, Any]
    parent: dict[str, Any] | None


class Environment:
    function = FunctionResolver.to_decorator()
    check = CheckResolver.to_decorator()
    command = CommandResolver.to_decorator()
    shell = ShellResolver.to_decorator()

    # TODO: default debug to False before release.
    def __init__(self, *, debug: bool = True):
        self.debug = debug
        self._converter: JsonConverter = make_converter()
        self._resolvers: dict[str, Resolver] = {}

    def __call__(self) -> None:
        configure_logging(logging.DEBUG if self.debug else logging.INFO)
        typer.run(self._entrypoint)

    def _entrypoint(
        self,
        register: Annotated[
            bool,
            typer.Option("-schema", help="Save environment and exit"),
        ] = False,  # noqa: FBT002
    ):
        try:
            anyio.run(self._run, self._register if register else self._serve)

        except cattrs.BaseValidationError as e:
            for error in cattrs.transform_error(e):
                errors.print(error)
            sys.exit(1)

        except FatalError as e:
            errors.print(e)
            sys.exit(1)

    async def _run(self, func):
        async with await dagger.connect():
            await func()

    async def _register(self):
        env = dagger.environment()

        for r in self._resolvers.values():
            try:
                env = r.register(env)
            except TypeError:  # noqa: PERF203
                logger.exception("Failed to register resolver %s", r.name)

        envid = await env.id()
        logger.debug("EnvironmentID = %s", envid)

        await envid_path.write_text(envid)

    async def _serve(self):
        inputs = self._converter.loads(await inputs_path.read_text(), Inputs)
        logger.debug("inputs = %s", inputs)

        # TODO: support type name
        _, field_name = inputs.resolver.split(".", 2)

        try:
            resolver = self._resolvers[field_name]
        except KeyError as e:
            msg = f'Invalid resolver name: "{field_name}"'
            raise FatalError(msg) from e

        # TODO: Support parent
        if inputs.parent:
            msg = "Resolver parent is not supported for now"
            raise FatalError(msg)

        try:
            output = await resolver.call(self._converter, inputs.args)
        except dagger.ExecError as e:
            rich.print(e.stdout)
            errors.print(e.stderr)
            sys.exit(e.exit_code)

        logger.debug("output = %s", output)
        await outputs_path.write_text(output)
