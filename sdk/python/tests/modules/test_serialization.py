import json
from typing import Annotated

import pytest

import dagger
from dagger.mod import Doc, Module


@pytest.mark.anyio()
async def test_unstructure_structure():
    mod = Module()

    @mod.object_type
    class Bar:
        msg: Annotated[str, Doc("Echo message")] = mod.field(default="foobar")
        ctr: Annotated[dagger.Container, Doc("A container")] = mod.field()

        @mod.function
        async def bar(self) -> str:
            return await self.ctr.with_exec(["echo", "-n", self.msg]).stdout()

    @mod.function
    def foo() -> Bar:
        return Bar(ctr=dagger.container().from_("alpine"))

    async with dagger.connection():
        resolver = mod.get_resolver(mod.get_resolvers("foo"), "Foo", "foo")
        result = await mod.get_result(resolver, dagger.JSON("{}"), {})

        parent = dagger.JSON(json.dumps(result))

        resolver = mod.get_resolver(mod.get_resolvers("foo"), "Bar", "bar")
        result = await mod.get_result(resolver, parent, {})

        assert result == "foobar"
