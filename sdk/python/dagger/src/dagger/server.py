import logging
from pathlib import Path
from typing import Any

from attrs import Factory, define
from cattrs import Converter
from strawberry import Schema

from .cli import Options, parse_args
from .converter import converter as json_converter
from .log import configure_logging

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
    options: Options = Factory(parse_args)
    debug: bool = False

    def execute(self) -> None:
        configure_logging(logging.DEBUG if self.debug else logging.INFO)

        if self.options.schema:
            logger.debug(f"schema => \n{self.schema}")
            self._write_schema()
            return

        inputs = self._read_inputs()
        logger.debug(f"{inputs = }")

        result = self._call_resolver(inputs)
        logger.debug(f"{result = }")

        self._write_output(result)

    def _write_schema(self) -> None:
        with schema_path.open("w") as f:
            f.write(str(self.schema))

    def _read_inputs(self) -> Inputs:
        with inputs_path.open("r") as f:
            return self.converter.loads(f.read(), Inputs)

    def _call_resolver(self, inputs: Inputs):
        type_name, field_name = inputs.resolver.split(".", 2)
        field = self.schema.get_field_for_type(field_name, type_name)
        resolver = self.schema.schema_converter.from_resolver(field)
        parent = field.origin(**inputs.parent) if inputs.parent else field.origin()
        return resolver(parent, info=None, **inputs.args)

    def _write_output(self, o) -> None:
        output = self.converter.dumps(o, ensure_ascii=False)
        logger.debug(f"{output = }")

        with outputs_path.open("w") as f:
            f.write(output)
