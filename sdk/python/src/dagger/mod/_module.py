# ruff: noqa: BLE001
import json
import logging
import sys
from collections.abc import Callable

import anyio
import cattrs
from graphql.pyutils import snake_to_camel
from rich.console import Console
from typing_extensions import overload

import dagger
from dagger.log import configure_logging

from ._converter import make_converter
from ._exceptions import FatalError, InternalError, UserError
from ._resolver import Func, Resolver
from ._utils import asyncify, transform_error

errors = Console(stderr=True, style="bold red")
logger = logging.getLogger(__name__)


class Module:
    """Builder for a :py:class:`dagger.Module`.

    Arguments
    ---------
    log_level:
        Configure logging with this minimal level. If `None`, logging
        is not configured.
    """

    # TODO: Hook debug from `--debug` flag in CLI?
    # TODO: Default logging to logging.WARNING before release.
    def __init__(self, *, log_level: int | str | None = logging.DEBUG):
        self._log_level = log_level
        self._converter: cattrs.Converter = make_converter()
        self._resolvers: dict[str, Resolver] = {}
        self._fn_call = dagger.current_function_call()
        self._mod = dagger.current_module()

    def add_resolver(self, resolver: Resolver):
        self._resolvers[resolver.graphql_name] = resolver

    def __call__(self) -> None:
        if self._log_level is not None:
            configure_logging(self._log_level)
        anyio.run(self._run)

    async def _run(self):
        async with await dagger.connect():
            await self._serve()

    async def _serve(self):
        name = await self._fn_call.name()
        result, exit_code = await (self._call(name) if name else self._register())
        try:
            output = json.dumps(result)
        except (TypeError, ValueError) as e:
            msg = f"Failed to serialize result: {e}"
            raise InternalError(msg) from e
        logger.debug("output => %s", repr(output))
        await self._fn_call.return_value(dagger.JSON(output))

        if exit_code:
            sys.exit(exit_code)

    async def _register(self) -> tuple[str, int]:
        # Resolvers are collected at import time, but only actually registered
        # during "serve".
        mod = self._mod

        # Get current module's name so users doesn't have to repeat it
        # themselves.
        # TODO: Support custom classes.
        mod_name = await mod.name()
        obj_def = dagger.type_def().with_object(
            snake_to_camel(mod_name.replace("-", "_"))
        )

        for r in self._resolvers.values():
            try:
                obj_def = r.register(obj_def)
            except TypeError as e:
                msg = f"Failed to register function `{r.name}`: {e}"
                raise UserError(msg) from e
            logger.debug("registered => %s", r.name)

        mod = mod.with_object(obj_def)

        return await mod.id(), 0

    async def _call(self, name: str) -> tuple[str, int]:
        try:
            resolver = self._resolvers[name]
        except KeyError as e:
            msg = f"Unable to find function “{name}”"
            raise FatalError(msg) from e

        logger.debug("resolver => %s", resolver.name)

        args = await self._fn_call.input_args()

        raw_args = {}
        for arg in args:
            arg_name = await arg.name()
            arg_value = await arg.value()
            try:
                # Cattrs can decode JSON strings but use `json` directly
                # for more granular control over the error.
                raw_args[arg_name] = json.loads(arg_value)
            except ValueError as e:
                msg = f"Unable to decode input argument `{arg_name}`: {e}"
                raise InternalError(msg) from e

        logger.debug("input args => %s", repr(raw_args))

        # Serialize/deserialize from here as this is the boundary that
        # manages the lifecycle through the API.
        kwargs = await resolver.convert_arguments(self._converter, raw_args)
        logger.debug("structured args => %s", repr(kwargs))

        try:
            result = await resolver(**kwargs)
        except Exception as e:
            logger.exception("Error during function execution")
            return str(e), 1

        logger.debug("result => %s", repr(result))

        try:
            result = await asyncify(
                self._converter.unstructure,
                result,
                resolver.return_type,
            )
        except Exception as e:
            msg = transform_error(
                e,
                "Failed to unstructure result",
                resolver.wrapped_func,
            )
            raise UserError(msg) from e

        logger.debug("unstructured result => %s", repr(result))

        return result, 0

    @overload
    def function(
        self,
        func: Func,
        *,
        name: None = None,
        description: None = None,
    ) -> Func:
        ...

    @overload
    def function(
        self,
        *,
        name: str | None = None,
        description: str | None = None,
    ) -> Callable[[Func], Func]:
        ...

    def function(
        self,
        func: Func | None = None,
        *,
        name: str | None = None,
        description: str | None = None,
    ) -> Func | Callable[[Func], Func]:
        def wrapper(func: Func) -> Func:
            r = Resolver.from_callable(func, name, description)
            self.add_resolver(r)
            return func

        return wrapper(func) if func else wrapper
