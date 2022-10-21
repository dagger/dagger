import logging
from pathlib import Path
from typing import Any

from attrs import define
from cattrs import Converter
from strawberry import Schema
from strawberry.utils.await_maybe import await_maybe

from .converter import converter as json_converter

logger = logging.getLogger(__name__)

inputs_path = Path("/inputs/dagger.json")
outputs_path = Path("/outputs/dagger.json")
schema_path = Path("/outputs/schema.graphql")


@define
class Inputs:
    resolver: str
    args: dict[str, Any]
    parent: dict[str, Any] | None


@define
class Server:
    schema: Schema
    converter: Converter = json_converter
    debug: bool = False

    def export_schema(self) -> None:
        logger.debug(f"schema => \n{self.schema}")
        schema_path.write_text(str(self.schema))

    async def execute(self) -> None:
        inputs = self._read_inputs()
        logger.debug(f"{inputs = }")

        result = await self._call_resolver(inputs)
        logger.debug(f"{result = }")

        self._write_output(result)

    def _read_inputs(self) -> Inputs:
        return self.converter.loads(inputs_path.read_text(), Inputs)

    async def _call_resolver(self, inputs: Inputs):
        type_name, field_name = inputs.resolver.split(".", 2)
        field = self.schema.get_field_for_type(field_name, type_name)
        resolver = self.schema.schema_converter.from_resolver(field)
        parent = field.origin(**inputs.parent) if inputs.parent else field.origin()
        return await await_maybe(resolver(parent, info=None, **inputs.args))

    def _write_output(self, o) -> None:
        output = self.converter.dumps(o, ensure_ascii=False)
        logger.debug(f"{output = }")
        outputs_path.write_text(output)
