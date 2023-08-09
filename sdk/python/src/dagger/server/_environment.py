import functools
import inspect
import logging
import sys
from collections.abc import Callable, Coroutine
from dataclasses import dataclass
from dataclasses import field as _
from typing import Annotated, Any, TypeAlias

import anyio
import rich
import typer
from cattrs.preconf.json import JsonConverter
from rich.console import Console

import dagger
from dagger.log import configure_logging

from ._converter import converter as json_converter
from ._converter import register_dagger_type_hooks
from ._exceptions import FatalError

CheckResolverFunc: TypeAlias = Callable[..., Coroutine[Any, Any, str]]

errors = Console(stderr=True)
logger = logging.getLogger(__name__)

inputs_path = anyio.Path("/inputs/dagger.json")
outputs_path = anyio.Path("/outputs/dagger.json")
envid_path = anyio.Path("/outputs/envid")


@dataclass(slots=True)
class Resolver:
    wrapped_func: CheckResolverFunc
    name: str
    description: str | None

    @classmethod
    def from_callable(
        cls,
        func: CheckResolverFunc,
        name: str | None = None,
        description: str | None = None,
    ):
        name = name or func.__name__
        description = description or inspect.getdoc(func)
        return cls(func, name, description)

    async def call(self, *args, **kwargs):
        # TODO: Use await_maybe to support non-async functions
        return await self.wrapped_func(*args, **kwargs)


@dataclass(slots=True)
class Inputs:
    resolver: str
    args: dict[str, Any]
    parent: dict[str, Any] | None


@dataclass(slots=True)
class Environment:
    debug: bool = True
    converter: JsonConverter = json_converter
    _resolvers: dict[str, Resolver] = _(init=False, default_factory=dict)

    def __call__(self) -> None:
        typer.run(self._entrypoint)

    def _entrypoint(
        self,
        register: Annotated[
            bool,
            typer.Option("-schema", help="Save environment and exit"),
        ] = False,  # noqa: FBT002
    ):
        configure_logging(logging.DEBUG if self.debug else logging.INFO)
        try:
            anyio.run(self._run, self._register if register else self._serve)
        except FatalError as e:
            errors.print(e)
            sys.exit(1)

    async def _run(self, func):
        async with await dagger.connect():
            await func()

    async def _register(self):
        env = dagger.environment()

        for r in self._resolvers.values():
            check = dagger.environment_check().with_name(r.name)
            if r.description:
                check = check.with_description(r.description)
            env = env.with_check(check)

        envid = await env.id()
        logger.debug("EnvironmentID = %s", envid)

        await envid_path.write_text(envid)

    async def _serve(self):
        inputs = self.converter.loads(await inputs_path.read_text(), Inputs)
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

        register_dagger_type_hooks(self.converter)

        try:
            result = await resolver.call(**inputs.args)
        except dagger.ExecError as e:
            rich.print(e.stdout)
            errors.print(e.stderr)
            sys.exit(e.exit_code)

        # cattrs is a sync library but we may need to use an
        # async function hook to convert the result so use to_thread
        # to coordinate this because from sync we can call from_thread.run.
        output = await anyio.to_thread.run_sync(
            functools.partial(
                self.converter.dumps,
                result,
                ensure_ascii=False,
            )
        )

        logger.debug("output = %s", output)
        await outputs_path.write_text(output)

    def check(
        self,
        resolver: CheckResolverFunc | None = None,
        *,
        name: str | None = None,
        description: str | None = None,
    ):
        def wrapper(func: CheckResolverFunc):
            r = Resolver.from_callable(func, name, description)
            self._resolvers[r.name] = r
            return r

        return wrapper(resolver) if resolver else wrapper
