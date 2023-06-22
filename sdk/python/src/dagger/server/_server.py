import logging
from collections.abc import Callable
from dataclasses import dataclass
from functools import partial
from pathlib import Path
from typing import Any, cast

import anyio
import strawberry
from cattrs.preconf.json import JsonConverter
from strawberry.utils.await_maybe import await_maybe

from ._context import Context
from ._converter import converter as json_converter
from ._converter import register_dagger_type_hooks

logger = logging.getLogger(__name__)

inputs_path = Path("/inputs/dagger.json")
outputs_path = Path("/outputs/dagger.json")
schema_path = Path("/outputs/schema.graphql")


@dataclass
class Inputs:
    resolver: str
    args: dict[str, Any]
    parent: dict[str, Any] | None


@dataclass
class Server:
    schema: strawberry.Schema
    converter: JsonConverter = json_converter
    debug: bool = False

    def export_schema(self) -> None:
        logger.debug("schema => \n%s", self.schema)
        schema_path.write_text(str(self.schema))

    def execute(self) -> None:
        inputs = self._read_inputs()
        logger.debug("inputs = %s", inputs)

        output = anyio.run(self._call_resolver, inputs)

        logger.debug("output = %s", output)
        self._write_output(output)

    def _read_inputs(self) -> Inputs:
        return self.converter.loads(inputs_path.read_text(), Inputs)

    def _write_output(self, out: str) -> None:
        outputs_path.write_text(out)

    async def _call_resolver(self, inputs: Inputs) -> str:
        type_name, field_name = inputs.resolver.split(".", 2)
        field = self.schema.get_field_for_type(field_name, type_name)
        if field is None:
            # TODO: use proper error class
            msg = f"Can't find field `{field_name}` for type `{type_name}`"
            raise ValueError(msg)

        resolver = self.schema.schema_converter.from_resolver(field)

        # origin is the parent type of the resolver/field.
        origin = cast(Callable, field.origin)

        async with Context() as context:
            register_dagger_type_hooks(self.converter, context)

            # inputs.parent is a dict of the parent type's fields.
            if inputs.parent is None:
                parent = origin()
            else:
                parent = await anyio.to_thread.run_sync(
                    self.converter.structure,
                    inputs.parent,
                    origin,
                )

            # Mock GraphQLResolveInfo that Strawberry wraps around in Info
            # so we can access some data in the decorated resolvers.
            #
            # Mocking because GraphQLResolveInfo is implemented as a
            # NamedTuple without defaults. We could fill those with as
            # much as we have here but it's not really necessary.
            # The type won't match but it's hidden in a dynamic layer so
            # the typing system won't complain.
            info = ResolveInfo(
                field_name=field.name,
                return_type=field.type,
                parent_type=field.origin,
                context=context,
            )

            result = await await_maybe(resolver(parent, info=info, **inputs.args))

            # cattrs is a sync library but we may need to use an
            # async function hook to convert the result so use to_thread
            # to coordinate this because from sync we can call from_thread.run.
            return await anyio.to_thread.run_sync(
                partial(self.converter.dumps, result, ensure_ascii=False),
            )


@dataclass
class ResolveInfo:
    field_name: str
    return_type: Any
    parent_type: Any
    context: Context
