import logging
from collections.abc import Callable
from pathlib import Path
from typing import Any, cast

import attrs
from cattrs.preconf.json import JsonConverter
from strawberry import Schema
from strawberry.utils.await_maybe import await_maybe

from .converter import converter as json_converter

logger = logging.getLogger(__name__)

inputs_path = Path("/inputs/dagger.json")
outputs_path = Path("/outputs/dagger.json")
schema_path = Path("/outputs/schema.graphql")


@attrs.define
class Inputs:
    resolver: str
    args: dict[str, Any]
    parent: dict[str, Any] | None


@attrs.define
class Server:
    schema: Schema
    converter: JsonConverter = json_converter
    debug: bool = False

    def export_schema(self) -> None:
        logger.debug("schema => \n%s", self.schema)
        schema_path.write_text(str(self.schema))

    async def execute(self) -> None:
        # No advantage in doing IO async here because this is the
        # entry point for the event loop. I.e., no other tasks
        # are running concurrently at this point.
        inputs = self._read_inputs()
        logger.debug("inputs = %s", inputs)

        # Resolvers can chose to be implemented in async,
        # e.g., running multiple client calls concurrently.
        result = await self._call_resolver(inputs)
        logger.debug("result = %s", result)

        self._write_output(result)

    def _read_inputs(self) -> Inputs:
        return self.converter.loads(inputs_path.read_text(), Inputs)

    async def _call_resolver(self, inputs: Inputs):
        type_name, field_name = inputs.resolver.split(".", 2)
        field = self.schema.get_field_for_type(field_name, type_name)
        if field is None:
            # FIXME: use proper error class
            msg = f"Can't find field `{field_name}` for type `{type_name}`"
            raise RuntimeError(msg)
        resolver: Callable = self.schema.schema_converter.from_resolver(field)
        origin = cast(Callable, field.origin)
        parent = origin(**inputs.parent) if inputs.parent else origin()
        return await await_maybe(resolver(parent, info=None, **inputs.args))

    def _write_output(self, o) -> None:
        output = self.converter.dumps(o, ensure_ascii=False)
        logger.debug("output = %s", output)
        outputs_path.write_text(output)
