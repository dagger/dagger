import logging
from collections.abc import Callable
from pathlib import Path
from typing import Any, cast

import anyio
import attrs
from cattrs.preconf.json import JsonConverter
from strawberry import Schema
from strawberry.utils.await_maybe import await_maybe

from dagger.api.gen import Client

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

    def execute(self) -> None:
        inputs = self._read_inputs()
        logger.debug("inputs = %s", inputs)

        # Resolvers can chose to be implemented in async,
        # e.g., running multiple client calls concurrently.
        result = anyio.run(self._call_resolver, inputs)
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
            raise ValueError(msg)
        resolver: Callable = self.schema.schema_converter.from_resolver(field)
        origin = cast(Callable, field.origin)
        parent = origin(**inputs.parent) if inputs.parent else origin()

        # Avoid a circular importissue.
        import dagger

        async with dagger.Connection() as client:
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
                context=Context(client),
            )
            return await await_maybe(resolver(parent, info=info, **inputs.args))

    def _write_output(self, o) -> None:
        output = self.converter.dumps(o, ensure_ascii=False)
        logger.debug("output = %s", output)
        outputs_path.write_text(output)


@attrs.define
class Context:
    client: Client


@attrs.define
class ResolveInfo:
    field_name: str
    return_type: Any
    parent_type: Any
    context: Context
