import contextlib
import sys

import anyio
from graphql import GraphQLSchema

import dagger

from . import codegen


def main():
    path = None
    match sys.argv[1:]:
        case ["generate", arg]:
            if arg != "-":
                path = anyio.Path(arg)
        case _:
            sys.stderr.write("usage: python -m dagger generate PATH\n")
            sys.exit(1)
    anyio.run(generate, path)


async def generate(output: anyio.Path | None = None):
    """Generate a client for the Dagger API."""
    schema = await _get_schema()
    code = codegen.generate(schema)

    if output:
        await output.write_text(code)
        await _update_gitattributes(output)
        sys.stdout.write(f"Client generated successfully to {output}\n")
    else:
        sys.stdout.write(f"{code}\n")


async def _get_schema() -> GraphQLSchema:
    # Get session because codegen is generating the client.
    async with contextlib.aclosing(dagger.Connection()) as connection:
        session = await connection.start()
        if not session.client.schema:
            msg = "Schema not initialized. Make sure the dagger engine is running."
            raise dagger.DaggerError(msg)
        return session.client.schema


async def _update_gitattributes(output: anyio.Path) -> None:
    git_attrs = output.with_name(".gitattributes")
    contents = f"/{output.name} linguist-generated=true\n"

    if await git_attrs.exists():
        if contents in (text := await git_attrs.read_text()):
            return
        contents = f"{text}{contents}"

    await git_attrs.write_text(contents)
