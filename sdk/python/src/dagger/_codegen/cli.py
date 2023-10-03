import sys

import anyio

from . import generator


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
    import dagger

    async with await dagger.connect() as conn:
        schema = await conn.session.get_schema()

    code = generator.generate(schema)

    if output:
        await output.write_text(code)
        await _update_gitattributes(output)
        sys.stdout.write(f"Client generated successfully to {output}\n")
    else:
        sys.stdout.write(f"{code}\n")


async def _update_gitattributes(output: anyio.Path) -> None:
    git_attrs = output.with_name(".gitattributes")
    contents = f"/{output.name} linguist-generated=true\n"

    if await git_attrs.exists():
        if contents in (text := await git_attrs.read_text()):
            return
        contents = f"{text}{contents}"

    await git_attrs.write_text(contents)
